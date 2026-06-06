package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

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
	fmt.Printf("role=%s room=%s user=%s\n", cfg.Role, cfg.RoomID, cfg.UserID)
	if err := http.ListenAndServe(cfg.Addr, app.Handler()); err != nil {
		log.Fatal(err)
	}
}
