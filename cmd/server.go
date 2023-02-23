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
)

// install server command
var cmdServer = &cobra.Command{
	Use:   "server",
	Short: "Run condition orchestrator API service",
	Run: func(cmd *cobra.Command, args []string) {
		app, err := app.New(cmd.Context(), model.AppKindServer, cfgFile, model.LogLevel(logLevel))
		if err != nil {
			log.Fatal(err)
		}

		repository, err := store.NewStore(cmd.Context(), *app.Config, app.Logger)
		if err != nil {
			app.Logger.Fatal(err)
		}

		srv := server.New(app.Logger, app.Config.ListenAddress, repository, app.NewEventStreamBrokerFromConfig())

		go func() {
			if err := srv.ListenAndServe(); err != nil && errors.Is(err, http.ErrServerClosed) {
				app.Logger.Fatal(err)
			}
		}()

		// sit around for term signal
		<-app.TermCh
		app.Logger.Info("got TERM signal, shutting down server...")

		// call server shutdown with timeout
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			app.Logger.Fatal("server shutdown error:", err)
		}

		// wait until context is done
		select {
		case <-ctx.Done():
			app.Logger.Info("server shutdown exceeded timeout")
		}

		app.Logger.Info("server shutdown complete.")
	},
}

// command server
type serverCmdFlags struct {
	listenAddress string
	storeKind     string
}

var (
	serverCmdFlagSet = &serverCmdFlags{}
)

// install command flags
func init() {

	cmdServer.PersistentFlags().StringVar(&serverCmdFlagSet.storeKind, "store", "serverservice", "Storage repository backend")

	rootCmd.AddCommand(cmdServer)
}
