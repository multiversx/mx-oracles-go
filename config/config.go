package config

import "github.com/ElrondNetwork/elrond-sdk-erdgo/aggregator/fetchers"

// PriceNotifierConfig price notifier configuration struct
type PriceNotifierConfig struct {
	GeneralConfig       GeneralNotifierConfig
	Pairs               []Pair
	MexTokenIDsMappings map[string]fetchers.MaiarTokensPair
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
}

// Pair parameters for a pair
type Pair struct {
	Base                      string
	Quote                     string
	PercentDifferenceToNotify uint32
	TrimPrecision             float64
	DenominationFactor        uint64
}
