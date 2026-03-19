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

	"github.com/XferOps/winnow/internal/api"
	"github.com/XferOps/winnow/internal/db"
	"github.com/XferOps/winnow/internal/embeddings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	pool, err := db.Connect(context.Background())
	if err != nil {
		log.Printf("Warning: database connection failed: %v", err)
		pool = nil
	}
	if pool != nil {
		defer pool.Close()
		if err := db.RunMigrations(context.Background(), pool); err != nil {
			log.Fatalf("Failed to run migrations: %v", err)
		}
	}

	embed, err := embeddings.NewClient()
	if err != nil {
		log.Printf("Warning: embeddings client init failed: %v", err)
		embed = nil
	}

	router := api.NewRouter(pool, embed)

	srv := &http.Server{
		Addr:        fmt.Sprintf(":%s", port),
		Handler:     router,
		ReadTimeout: 15 * time.Second,
		// SSE endpoints can legitimately stay open while incremental progress
		// events are streamed, so a fixed write timeout will cut them off.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Hizal server starting on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server stopped")
}
