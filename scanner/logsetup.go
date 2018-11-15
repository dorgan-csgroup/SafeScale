package main

import (
	"github.com/CS-SI/SafeScale/utils"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
)

func init() {
	log.SetFormatter(&utils.MyFormatter{ TextFormatter: log.TextFormatter{ForceColors:true, TimestampFormat : "2006-01-02 15:04:05", FullTimestamp:true, DisableLevelTruncation:true}})
	log.SetLevel(log.DebugLevel)

	// Log as JSON instead of the default ASCII formatter.
	// log.SetFormatter(&log.JSONFormatter{})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	file, err := os.OpenFile("scanner-session.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}

	log.SetOutput(io.MultiWriter(os.Stdout, file))
}