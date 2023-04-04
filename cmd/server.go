package cmd

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/server"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/spf13/cobra"
	"go.hollow.sh/toolbox/events"
)

var (
	shutdownTimeout = 10 * time.Second
)

// install server command
var cmdServer = &cobra.Command{
	Use:   "server",
	Short: "Run condition orchestrator API service",
	Run: func(cmd *cobra.Command, args []string) {
		app, termCh, err := app.New(cmd.Context(), model.AppKindServer, cfgFile, model.LogLevel(logLevel))
		if err != nil {
			log.Fatal(err)
		}

		repository, err := store.NewStore(cmd.Context(), app.Config, app.Config.ConditionDefinitions, app.Logger)
		if err != nil {
			app.Logger.Fatal(err)
		}

		streamBroker, err := events.NewStream(app.Config.NatsOptions)
		if err != nil {
			app.Logger.Fatal(err)
		}

		if err := streamBroker.Open(); err != nil {
			app.Logger.Fatal(err)
		}

		options := []server.Option{
			server.WithLogger(app.Logger),
			server.WithListenAddress(app.Config.ListenAddress),
			server.WithStore(repository),
			server.WithStreamBroker(streamBroker),
			server.WithConditionDefinitions(app.Config.ConditionDefinitions),
		}

		srv := server.New(options...)
		go func() {
			if err := srv.ListenAndServe(); err != nil && errors.Is(err, http.ErrServerClosed) {
				app.Logger.Fatal(err)
			}
		}()

		// sit around for term signal
		<-termCh
		app.Logger.Info("got TERM signal, shutting down server...")

		// call server shutdown with timeout
		ctx, cancel := context.WithTimeout(cmd.Context(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			app.Logger.Fatal("server shutdown error:", err)
		}

		// wait until context is done
		<-ctx.Done()
		app.Logger.Info("server shutdown exceeded timeout")

		app.Logger.Info("server shutdown complete.")
	},
}

// install command flags
func init() {
	rootCmd.AddCommand(cmdServer)
}
