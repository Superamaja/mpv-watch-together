package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mpv-watch-together/helper/internal/config"
	"mpv-watch-together/helper/internal/server"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	app, err := server.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	fmt.Printf("mpv-watch-helper listening on http://%s\n", cfg.Addr)
	room := cfg.RoomID
	if room == "" {
		room = "(waiting for mpv config)"
	}
	fmt.Printf("role=%s room=%s user=%s\n", cfg.Role, room, cfg.UserID)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		errs <- httpServer.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-signals:
		fmt.Println("shutting down mpv-watch-helper")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Fatal(err)
		}
	}
}
