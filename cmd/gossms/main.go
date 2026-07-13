package main

import (
	"log"
	"os"

	"github.com/radix29/gossms/internal/tui"
)

func main() {
	logFile, err := os.OpenFile("gossms.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	app := tui.NewApp()
	if err := app.Run(); err != nil {
		log.Fatalf("gossms error: %v", err)
	}
}
