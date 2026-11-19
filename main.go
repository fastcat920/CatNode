package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wyx2685/v2node/cmd"
	"github.com/wyx2685/v2node/internal/fastcatv2"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fastcat-v2" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := fastcatv2.Run(ctx, os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	cmd.Run()
}
