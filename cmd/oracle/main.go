package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	chainCore "github.com/multiversx/mx-chain-core-go/core"
	"github.com/multiversx/mx-chain-core-go/core/check"
	"github.com/multiversx/mx-chain-crypto-go/signing"
	"github.com/multiversx/mx-chain-crypto-go/signing/ed25519"
	chainFactory "github.com/multiversx/mx-chain-go/cmd/node/factory"
	chainCommon "github.com/multiversx/mx-chain-go/common"
	logger "github.com/multiversx/mx-chain-logger-go"
	"github.com/multiversx/mx-chain-logger-go/file"
	"github.com/multiversx/mx-oracles-go/config"
	"github.com/multiversx/mx-sdk-go/aggregator"
	"github.com/multiversx/mx-sdk-go/aggregator/api/gin"
	"github.com/multiversx/mx-sdk-go/aggregator/fetchers"
	"github.com/multiversx/mx-sdk-go/aggregator/notifees"
	"github.com/multiversx/mx-sdk-go/authentication"
	"github.com/multiversx/mx-sdk-go/blockchain"
	"github.com/multiversx/mx-sdk-go/blockchain/cryptoProvider"
	"github.com/multiversx/mx-sdk-go/builders"
	sdkCore "github.com/multiversx/mx-sdk-go/core"
	"github.com/multiversx/mx-sdk-go/core/polling"
	"github.com/multiversx/mx-sdk-go/data"
	"github.com/multiversx/mx-sdk-go/interactors"
	"github.com/multiversx/mx-sdk-go/interactors/nonceHandlerV2"
	"github.com/multiversx/mx-sdk-go/workflows"
	"github.com/urfave/cli"
)

const (
	defaultLogsPath = "logs"
	logFilePrefix   = "mx-oracle"
)

var log = logger.GetOrCreate("priceFeeder/main")

// appVersion should be populated at build time using ldflags
// Usage examples:
// linux/mac:
//            go build -i -v -ldflags="-X main.appVersion=$(git describe --tags --long --dirty)"
// windows:
//            for /f %i in ('git describe --tags --long --dirty') do set VERS=%i
//            go build -i -v -ldflags="-X main.appVersion=%VERS%"
var appVersion = chainCommon.UnVersionedAppString

func main() {
	app := cli.NewApp()
	app.Name = "Relay CLI app"
	app.Usage = "Price feeder will fetch the price of a defined pair from a bunch of exchanges, and will" +
		" write to the contract if the price changed"
	app.Flags = getFlags()
	machineID := chainCore.GetAnonymizedMachineID(app.Name)
	app.Version = fmt.Sprintf("%s/%s/%s-%s/%s", appVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, machineID)
	app.Authors = []cli.Author{
		{
			Name:  "The MultiversX Team",
			Email: "contact@multiversx.com",
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
		logsCfg := cfg.GeneralConfig.Logs
		timeLogLifeSpan := time.Second * time.Duration(logsCfg.LogFileLifeSpanInSec)
		sizeLogLifeSpanInMB := uint64(logsCfg.LogFileLifeSpanInMB)
		err = fileLogging.ChangeFileLifeSpan(timeLogLifeSpan, sizeLogLifeSpanInMB)
		if err != nil {
			return err
		}
	}

	for key, val := range cfg.XExchangeTokenIDsMappings {
		log.Info("read xExchange token IDs mapping", "key", key, "quote", val.Quote, "base", val.Base)
	}

	if len(cfg.GeneralConfig.NetworkAddress) == 0 {
		return fmt.Errorf("empty NetworkAddress in config file")
	}

	argsProxy := blockchain.ArgsProxy{
		ProxyURL:            cfg.GeneralConfig.NetworkAddress,
		SameScState:         false,
		ShouldBeSynced:      false,
		FinalityCheck:       cfg.GeneralConfig.ProxyFinalityCheck,
		AllowedDeltaToFinal: cfg.GeneralConfig.ProxyMaxNoncesDelta,
		CacheExpirationTime: time.Second * time.Duration(cfg.GeneralConfig.ProxyCacherExpirationSeconds),
		EntityType:          sdkCore.RestAPIEntityType(cfg.GeneralConfig.ProxyRestAPIEntityType),
	}
	proxy, err := blockchain.NewProxy(argsProxy)
	if err != nil {
		return err
	}

	txBuilder, err := builders.NewTxBuilder(cryptoProvider.NewSigner())
	if err != nil {
		return err
	}

	args := nonceHandlerV2.ArgsNonceTransactionsHandlerV2{
		Proxy:            proxy,
		IntervalToResend: time.Second * time.Duration(cfg.GeneralConfig.IntervalToResendTxsInSeconds),
		Creator:          &nonceHandlerV2.SingleTransactionAddressNonceHandlerCreator{},
	}
	txNonceHandler, err := nonceHandlerV2.NewNonceTransactionHandlerV2(args)
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

	cryptoHolder, err := cryptoProvider.NewCryptoComponentsHolder(keyGen, privateKeyBytes)
	if err != nil {
		return err
	}

	authClient, err := createAuthClient(proxy, cryptoHolder, cfg.AuthenticationConfig)
	if err != nil {
		return err
	}

	graphqlResponseGetter, err := aggregator.NewGraphqlResponseGetter(authClient)
	if err != nil {
		return err
	}

	httpResponseGetter, err := aggregator.NewHttpResponseGetter()
	if err != nil {
		return err
	}

	priceFetchers, err := createPriceFetchers(httpResponseGetter, graphqlResponseGetter, cfg.XExchangeTokenIDsMappings)
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

	if err != nil {
		return err
	}
	argsNotifee := notifees.ArgsMxNotifee{
		Proxy:           proxy,
		TxBuilder:       txBuilder,
		TxNonceHandler:  txNonceHandler,
		ContractAddress: aggregatorAddress,
		BaseGasLimit:    cfg.GeneralConfig.BaseGasLimit,
		GasLimitForEach: cfg.GeneralConfig.GasLimitForEach,
		CryptoHolder:    cryptoHolder,
	}
	mxNotifee, err := notifees.NewMxNotifee(argsNotifee)
	if err != nil {
		return err
	}

	argsPriceNotifier := aggregator.ArgsPriceNotifier{
		Pairs:            []*aggregator.ArgsPair{},
		Aggregator:       priceAggregator,
		Notifee:          mxNotifee,
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

	log.Info("Starting MultiversX Notifee")

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
	err := chainCore.LoadTomlFile(&cfg, filepath)
	if err != nil {
		return config.PriceNotifierConfig{}, err
	}

	return cfg, nil
}

func createPriceFetchers(httpReponseGetter aggregator.ResponseGetter, graphqlResponseGetter aggregator.GraphqlGetter, tokenIdsMappings map[string]fetchers.XExchangeTokensPair) ([]aggregator.PriceFetcher, error) {
	exchanges := fetchers.ImplementedFetchers
	priceFetchers := make([]aggregator.PriceFetcher, 0, len(exchanges))

	for exchangeName := range exchanges {
		priceFetcher, err := fetchers.NewPriceFetcher(exchangeName, httpReponseGetter, graphqlResponseGetter, tokenIdsMappings)
		if err != nil {
			return nil, err
		}

		priceFetchers = append(priceFetchers, priceFetcher)
	}

	return priceFetchers, nil
}

func createAuthClient(proxy workflows.ProxyHandler, cryptoHolder sdkCore.CryptoComponentsHolder, config config.AuthenticationConfig) (authentication.AuthClient, error) {
	args := authentication.ArgsNativeAuthClient{
		Signer:                 cryptoProvider.NewSigner(),
		ExtraInfo:              nil,
		Proxy:                  proxy,
		TokenExpiryInSeconds:   uint64(config.TokenExpiryInSeconds),
		Host:                   config.Host,
		CryptoComponentsHolder: cryptoHolder,
	}

	authClient, err := authentication.NewNativeAuthClient(args)
	if err != nil {
		return nil, err
	}

	return authClient, nil
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
func attachFileLogger(log logger.Logger, flagsConfig config.ContextFlagsConfig) (chainFactory.FileLoggingHandler, error) {
	var fileLogging chainFactory.FileLoggingHandler
	var err error
	if flagsConfig.SaveLogFile {
		args := file.ArgsFileLogging{
			WorkingDir:      flagsConfig.WorkingDir,
			DefaultLogsPath: defaultLogsPath,
			LogFilePrefix:   logFilePrefix,
		}
		fileLogging, err = file.NewFileLogging(args)
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
