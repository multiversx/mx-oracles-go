package config

import "github.com/multiversx/mx-sdk-go/aggregator/fetchers"

// PriceNotifierConfig price notifier configuration struct
type PriceNotifierConfig struct {
	GeneralConfig        GeneralNotifierConfig
	AuthenticationConfig AuthenticationConfig
	Pairs                []Pair
	MexTokenIDsMappings  map[string]fetchers.MaiarTokensPair
}

// GeneralNotifierConfig general price notifier configuration struct
type GeneralNotifierConfig struct {
	NetworkAddress               string
	PrivateKeyFile               string
	IntervalToResendTxsInSeconds uint64
	ProxyCacherExpirationSeconds uint64
	AggregatorContractAddress    string
	BaseGasLimit                 uint64
	GasLimitForEach              uint64
	MinResultsNum                int
	PollIntervalInSeconds        uint64
	AutoSendIntervalInSeconds    uint64
	ProxyRestAPIEntityType       string
	ProxyMaxNoncesDelta          int
	ProxyFinalityCheck           bool
	Logs                         LogsConfig
}

// LogsConfig will hold settings related to the logging sub-system
type LogsConfig struct {
	LogFileLifeSpanInSec int
	LogFileLifeSpanInMB  int
}

// AuthenticationConfig authentication configuration struct
type AuthenticationConfig struct {
	TokenExpiryInSeconds int
	Host                 string
}

// Pair parameters for a pair
type Pair struct {
	Base                      string
	Quote                     string
	PercentDifferenceToNotify uint32
	Decimals                  uint64
	Exchanges                 []string
}

// ContextFlagsConfig holds the configuration for flags
type ContextFlagsConfig struct {
	WorkingDir        string
	LogLevel          string
	DisableAnsiColor  bool
	ConfigurationFile string
	SaveLogFile       bool
	EnableLogName     bool
	RestApiInterface  string
}
