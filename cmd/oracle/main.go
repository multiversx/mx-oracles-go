package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	elrondCore "github.com/ElrondNetwork/elrond-go-core/core"
	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-crypto/signing"
	"github.com/ElrondNetwork/elrond-go-crypto/signing/ed25519"
	logger "github.com/ElrondNetwork/elrond-go-logger"
	elrondFactory "github.com/ElrondNetwork/elrond-go/cmd/node/factory"
	elrondCommon "github.com/ElrondNetwork/elrond-go/common"
	"github.com/ElrondNetwork/elrond-go/common/logging"
	"github.com/ElrondNetwork/elrond-oracle/config"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/aggregator"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/aggregator/api/gin"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/aggregator/fetchers"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/aggregator/notifees"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/blockchain"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/builders"
	erdgoCore "github.com/ElrondNetwork/elrond-sdk-erdgo/core"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/core/polling"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/data"
	"github.com/ElrondNetwork/elrond-sdk-erdgo/interactors"
	"github.com/urfave/cli"
)

const (
	defaultLogsPath = "logs"
	logFilePrefix   = "elrond-eth-bridge"
)

var log = logger.GetOrCreate("priceFeeder/main")

// appVersion should be populated at build time using ldflags
// Usage examples:
// linux/mac:
//            go build -i -v -ldflags="-X main.appVersion=$(git describe --tags --long --dirty)"
// windows:
//            for /f %i in ('git describe --tags --long --dirty') do set VERS=%i
//            go build -i -v -ldflags="-X main.appVersion=%VERS%"
var appVersion = elrondCommon.UnVersionedAppString

func main() {
	app := cli.NewApp()
	app.Name = "Relay CLI app"
	app.Usage = "Price feeder will fetch the price of a defined pair from a bunch of exchanges, and will" +
		" write to the contract if the price changed"
	app.Flags = getFlags()
	machineID := elrondCore.GetAnonymizedMachineID(app.Name)
	app.Version = fmt.Sprintf("%s/%s/%s-%s/%s", appVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, machineID)
	app.Authors = []cli.Author{
		{
			Name:  "The Elrond Team",
			Email: "contact@elrond.com",
		},
	}

	app.Action = func(c *cli.Context) error {
		return startOracle(c, app.Version)
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func startOracle(ctx *cli.Context, version string) error {
	flagsConfig := getFlagsConfig(ctx)

	fileLogging, errLogger := attachFileLogger(log, flagsConfig)
	if errLogger != nil {
		return errLogger
	}

	log.Info("starting oracle node", "version", version, "pid", os.Getpid())

	err := logger.SetLogLevel(flagsConfig.LogLevel)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(flagsConfig.ConfigurationFile)
	if err != nil {
		return err
	}

	if !check.IfNil(fileLogging) {
		err = fileLogging.ChangeFileLifeSpan(time.Second * time.Duration(cfg.GeneralConfig.LogFileLifeSpanInSec))
		if err != nil {
			return err
		}
	}

	for key, val := range cfg.MexTokenIDsMappings {
		log.Info("read mex token IDs mapping", "key", key, "quote", val.Quote, "base", val.Base)
	}

	if len(cfg.GeneralConfig.NetworkAddress) == 0 {
		return fmt.Errorf("empty NetworkAddress in config file")
	}

	args := blockchain.ArgsElrondProxy{
		ProxyURL:            cfg.GeneralConfig.NetworkAddress,
		SameScState:         false,
		ShouldBeSynced:      false,
		FinalityCheck:       cfg.GeneralConfig.ProxyFinalityCheck,
		AllowedDeltaToFinal: cfg.GeneralConfig.ProxyMaxNoncesDelta,
		CacheExpirationTime: time.Second * time.Duration(cfg.GeneralConfig.ProxyCacherExpirationSeconds),
		EntityType:          erdgoCore.RestAPIEntityType(cfg.GeneralConfig.ProxyRestAPIEntityType),
	}
	proxy, err := blockchain.NewElrondProxy(args)
	if err != nil {
		return err
	}

	priceFetchers, err := createPriceFetchers(cfg.MexTokenIDsMappings)
	if err != nil {
		return err
	}

	argsPriceAggregator := aggregator.ArgsPriceAggregator{
		PriceFetchers: priceFetchers,
		MinResultsNum: cfg.GeneralConfig.MinResultsNum,
	}
	priceAggregator, err := aggregator.NewPriceAggregator(argsPriceAggregator)
	if err != nil {
		return err
	}

	txBuilder, err := builders.NewTxBuilder(blockchain.NewTxSigner())
	if err != nil {
		return err
	}

	txNonceHandler, err := interactors.NewNonceTransactionHandler(proxy, time.Second*time.Duration(cfg.GeneralConfig.IntervalToResendTxsInSeconds), true)
	if err != nil {
		return err
	}

	aggregatorAddress, err := data.NewAddressFromBech32String(cfg.GeneralConfig.AggregatorContractAddress)
	if err != nil {
		return err
	}

	var keyGen = signing.NewKeyGenerator(ed25519.NewEd25519())
	wallet := interactors.NewWallet()
	privateKeyBytes, err := wallet.LoadPrivateKeyFromPemFile(cfg.GeneralConfig.PrivateKeyFile)
	if err != nil {
		return err
	}

	privateKey, err := keyGen.PrivateKeyFromByteArray(privateKeyBytes)

	if err != nil {
		return err
	}
	argsElrondNotifee := notifees.ArgsElrondNotifee{
		Proxy:           proxy,
		TxBuilder:       txBuilder,
		TxNonceHandler:  txNonceHandler,
		ContractAddress: aggregatorAddress,
		PrivateKey:      privateKey,
		BaseGasLimit:    cfg.GeneralConfig.BaseGasLimit,
		GasLimitForEach: cfg.GeneralConfig.GasLimitForEach,
	}
	elrondNotifee, err := notifees.NewElrondNotifee(argsElrondNotifee)
	if err != nil {
		return err
	}

	argsPriceNotifier := aggregator.ArgsPriceNotifier{
		Pairs:            []*aggregator.ArgsPair{},
		Aggregator:       priceAggregator,
		Notifee:          elrondNotifee,
		AutoSendInterval: time.Second * time.Duration(cfg.GeneralConfig.AutoSendIntervalInSeconds),
	}
	for _, pair := range cfg.Pairs {
		argsPair := aggregator.ArgsPair{
			Base:                      pair.Base,
			Quote:                     pair.Quote,
			PercentDifferenceToNotify: pair.PercentDifferenceToNotify,
			Decimals:                  pair.Decimals,
			Exchanges:                 getMapFromSlice(pair.Exchanges),
		}
		addPairToFetchers(argsPair, priceFetchers)
		argsPriceNotifier.Pairs = append(argsPriceNotifier.Pairs, &argsPair)
	}
	priceNotifier, err := aggregator.NewPriceNotifier(argsPriceNotifier)
	if err != nil {
		return err
	}

	argsPollingHandler := polling.ArgsPollingHandler{
		Log:              log,
		Name:             "price notifier polling handler",
		PollingInterval:  time.Second * time.Duration(cfg.GeneralConfig.PollIntervalInSeconds),
		PollingWhenError: time.Second * time.Duration(cfg.GeneralConfig.PollIntervalInSeconds),
		Executor:         priceNotifier,
	}

	pollingHandler, err := polling.NewPollingHandler(argsPollingHandler)
	if err != nil {
		return err
	}

	httpServerWrapper, err := gin.NewWebServerHandler(flagsConfig.RestApiInterface)
	if err != nil {
		return err
	}

	err = httpServerWrapper.StartHttpServer()
	if err != nil {
		return err
	}

	log.Info("Starting Elrond Notifee")

	err = pollingHandler.StartProcessingLoop()
	if err != nil {
		return err
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-sigs

	log.Info("application closing, closing polling handler...")

	err = pollingHandler.Close()
	return err
}

func loadConfig(filepath string) (config.PriceNotifierConfig, error) {
	cfg := config.PriceNotifierConfig{}
	err := elrondCore.LoadTomlFile(&cfg, filepath)
	if err != nil {
		return config.PriceNotifierConfig{}, err
	}

	return cfg, nil
}

func createPriceFetchers(tokenIdsMappings map[string]fetchers.MaiarTokensPair) ([]aggregator.PriceFetcher, error) {
	exchanges := fetchers.ImplementedFetchers
	priceFetchers := make([]aggregator.PriceFetcher, 0, len(exchanges))
	for exchangeName := range exchanges {
		priceFetcher, err := fetchers.NewPriceFetcher(exchangeName, &aggregator.HttpResponseGetter{}, tokenIdsMappings)
		if err != nil {
			return nil, err
		}

		priceFetchers = append(priceFetchers, priceFetcher)
	}

	return priceFetchers, nil
}

func addPairToFetchers(argsPair aggregator.ArgsPair, priceFetchers []aggregator.PriceFetcher) {
	for _, fetcher := range priceFetchers {
		_, ok := argsPair.Exchanges[fetcher.Name()]
		if ok {
			fetcher.AddPair(argsPair.Base, argsPair.Quote)
		}
	}
}

func getMapFromSlice(exchangesSlice []string) map[string]struct{} {
	exchangesMap := make(map[string]struct{})
	for _, exchange := range exchangesSlice {
		exchangesMap[exchange] = struct{}{}
	}
	return exchangesMap
}

// TODO: EN-12835 extract this into core
func attachFileLogger(log logger.Logger, flagsConfig config.ContextFlagsConfig) (elrondFactory.FileLoggingHandler, error) {
	var fileLogging elrondFactory.FileLoggingHandler
	var err error
	if flagsConfig.SaveLogFile {
		fileLogging, err = logging.NewFileLogging(flagsConfig.WorkingDir, defaultLogsPath, logFilePrefix)
		if err != nil {
			return nil, fmt.Errorf("%w creating a log file", err)
		}
	}

	err = logger.SetDisplayByteSlice(logger.ToHex)
	log.LogIfError(err)
	logger.ToggleLoggerName(flagsConfig.EnableLogName)
	logLevelFlagValue := flagsConfig.LogLevel
	err = logger.SetLogLevel(logLevelFlagValue)
	if err != nil {
		return nil, err
	}

	if flagsConfig.DisableAnsiColor {
		err = logger.RemoveLogObserver(os.Stdout)
		if err != nil {
			return nil, err
		}

		err = logger.AddLogObserver(os.Stdout, &logger.PlainFormatter{})
		if err != nil {
			return nil, err
		}
	}
	log.Trace("logger updated", "level", logLevelFlagValue, "disable ANSI color", flagsConfig.DisableAnsiColor)

	return fileLogging, nil
}
