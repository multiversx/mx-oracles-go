package main

import (
	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/facade"
	"github.com/ElrondNetwork/elrond-oracle/config"
	"github.com/urfave/cli"
)

var (
	logLevel = cli.StringFlag{
		Name: "log-level",
		Usage: "This flag specifies the logger `level(s)`. It can contain multiple comma-separated value. For example" +
			", if set to *:INFO the logs for all packages will have the INFO level. However, if set to *:INFO,api:DEBUG" +
			" the logs for all packages will have the INFO level, excepting the api package which will receive a DEBUG" +
			" log level.",
		Value: "*:" + logger.LogDebug.String(),
	}
	configurationFile = cli.StringFlag{
		Name: "config",
		Usage: "The `[path]` for the main configuration file. This TOML file contains the main " +
			"configurations such as storage setups, epoch duration and so on.",
		Value: "config/config.toml",
	}
	logSaveFile = cli.BoolFlag{
		Name:  "log-save",
		Usage: "Boolean option for enabling log saving. If set, it will automatically save all the logs into a file.",
	}
	restApiInterface = cli.StringFlag{
		Name: "rest-api-interface",
		Usage: "The interface `address and port` to which the REST API will attempt to bind. " +
			"To bind to all available interfaces, set this flag to :8080",
		Value: facade.DefaultRestInterface,
	}
	workingDirectory = cli.StringFlag{
		Name:  "working-directory",
		Usage: "This flag specifies the `directory` where the node will store databases and logs.",
		Value: "",
	}
	disableAnsiColor = cli.BoolFlag{
		Name:  "disable-ansi-color",
		Usage: "Boolean option for disabling ANSI colors in the logging system.",
	}
	logWithLoggerName = cli.BoolFlag{
		Name:  "log-logger-name",
		Usage: "Boolean option for logger name in the logs.",
	}
)

func getFlags() []cli.Flag {
	return []cli.Flag{
		workingDirectory,
		logLevel,
		disableAnsiColor,
		configurationFile,
		logSaveFile,
		logWithLoggerName,
		restApiInterface,
	}
}
func getFlagsConfig(ctx *cli.Context) config.ContextFlagsConfig {
	flagsConfig := config.ContextFlagsConfig{}

	flagsConfig.WorkingDir = ctx.GlobalString(workingDirectory.Name)
	flagsConfig.LogLevel = ctx.GlobalString(logLevel.Name)
	flagsConfig.DisableAnsiColor = ctx.GlobalBool(disableAnsiColor.Name)
	flagsConfig.ConfigurationFile = ctx.GlobalString(configurationFile.Name)
	flagsConfig.SaveLogFile = ctx.GlobalBool(logSaveFile.Name)
	flagsConfig.EnableLogName = ctx.GlobalBool(logWithLoggerName.Name)
	flagsConfig.RestApiInterface = ctx.GlobalString(restApiInterface.Name)

	return flagsConfig
}
