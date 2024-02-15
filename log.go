package rtcnet

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// By default use the global logger
var logger = log.Logger

func SetLogger(newLogger zerolog.Logger) {
	logger = newLogger
}

// Old helper functions. Note I left this here so that I could easily just disable it. But I should probably remove this and use zerolog log levels
func trace(msg string) {
	logger.Trace().Msg(msg)
}
