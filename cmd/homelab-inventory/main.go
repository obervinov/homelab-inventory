// homelab-inventory answers one question: what version of what is running
// where. Hosts push their versions, the cluster is scraped, and one internal
// endpoint returns the lot with each item mapped to its upstream repository.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/obervinov/homelab-inventory/internal/config"
	"github.com/obervinov/homelab-inventory/internal/kube"
	"github.com/obervinov/homelab-inventory/internal/store"
	"github.com/obervinov/homelab-inventory/internal/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(env("INV_CONFIG", "/etc/homelab-inventory/config.json"))
	if err != nil {
		log.Error("load config", "error", err)
		os.Exit(1)
	}
	st := store.New()
	srv := &web.Server{Cfg: cfg, Store: st, TrustForwardedFor: env("INV_TRUST_FORWARDED_FOR", "false") == "true"}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Kubernetes.Enabled {
		col, err := kube.InCluster()
		if err != nil {
			log.Error("kubernetes collector", "error", err)
			os.Exit(1)
		}
		go func() {
			tick := time.NewTicker(cfg.KubeInterval())
			defer tick.Stop()
			for {
				c, cancel := context.WithTimeout(ctx, time.Minute)
				items, err := col.Collect(c)
				cancel()
				if err != nil {
					// Keep the previous snapshot; staleness will surface a long outage.
					log.Error("kubernetes collect", "error", err)
				} else {
					st.Replace(cfg.Kubernetes.Source, items, time.Now())
					log.Info("kubernetes collect", "items", len(items))
				}
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}()
	}

	servers := []*http.Server{
		{Addr: env("INV_PUBLIC_ADDR", ":8080"), Handler: srv.Public()},
		{Addr: env("INV_INTERNAL_ADDR", ":8081"), Handler: srv.Internal()},
	}
	for _, s := range servers {
		s.ReadHeaderTimeout, s.ReadTimeout, s.WriteTimeout = 5*time.Second, 15*time.Second, 30*time.Second
		go func(s *http.Server) {
			log.Info("listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("serve", "addr", s.Addr, "error", err)
				stop()
			}
		}(s)
	}
	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		s.Shutdown(shut)
	}
}
