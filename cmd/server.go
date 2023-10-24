package cmd

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/equinix-labs/otel-init-go/otelinit"

	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/server"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/ginjwt"
)

var shutdownTimeout = 10 * time.Second

// install server command
var cmdServer = &cobra.Command{
	Use:   "server",
	Short: "Run condition orchestrator API service",
	Run: func(cmd *cobra.Command, args []string) {
		app, termCh, err := app.New(cmd.Context(), model.AppKindServer, cfgFile, model.LogLevel(logLevel))
		if err != nil {
			log.Fatal(err)
		}

		streamBroker, err := events.NewStream(app.Config.NatsOptions)
		if err != nil {
			app.Logger.Fatal(err)
		}

		if err = streamBroker.Open(); err != nil {
			app.Logger.Fatal(err)
		}

		// setup cancel context with cancel func
		ctx, serverCancel := context.WithCancel(cmd.Context())

		repository, err := store.NewStore(ctx, app.Config, app.Config.ConditionDefinitions, app.Logger, streamBroker)
		if err != nil {
			app.Logger.Fatal(err)
		}

		// serve metrics on port 9090
		metrics.ListenAndServe()

		// the ignored parameter here is a context annotated with otel-init-go configuration
		_, otelShutdown := otelinit.InitOpenTelemetry(cmd.Context(), "conditionorc-api-server")

		options := []server.Option{
			server.WithLogger(app.Logger),
			server.WithListenAddress(app.Config.ListenAddress),
			server.WithStore(repository),
			server.WithStreamBroker(streamBroker),
			server.WithConditionDefinitions(app.Config.ConditionDefinitions),
		}

		if viper.GetViper().GetBool("oidc.enabled") {
			app.Logger.Info("enabling OIDC")
			options = append(options, server.WithAuthMiddlewareConfig(app.Config.APIServerJWTAuth))
		} else {
			app.Logger.Info("OIDC disabled")
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
		serverCancel()

		// call server shutdown with timeout
		ctx, cancel := context.WithTimeout(cmd.Context(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			app.Logger.Fatal("server shutdown error:", err)
		}
		otelShutdown(ctx)
	},
}

// install command flags
func init() {
	rootCmd.AddCommand(cmdServer)
	cmdServer.Flags().Bool("oidc", true, "use oidc auth")
	ginjwt.BindFlagFromViperInst(viper.GetViper(), "oidc.enabled", cmdServer.Flags().Lookup("oidc"))
}
