package cmd

import (
	"log"

	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/orchestrator"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/spf13/cobra"
	"go.hollow.sh/toolbox/events"
)

// install orchestrator command
var cmdOrchestrator = &cobra.Command{
	Use:   "orchestrator",
	Short: "Run condition orchestrator service",
	Run: func(cmd *cobra.Command, args []string) {
		app, err := app.New(cmd.Context(), model.AppKindOrchestrator, cfgFile, model.LogLevel(logLevel))
		if err != nil {
			log.Fatal(err)
		}

		repository, err := store.NewStore(cmd.Context(), app.Config, app.Config.ConditionDefinitions, app.Logger)
		if err != nil {
			app.Logger.Fatal(err)
		}

		streamBroker, err := events.NewStreamBroker(app.Config.NatsOptions)
		if err != nil {
			app.Logger.Fatal(err)
		}

		if err := streamBroker.Open(); err != nil {
			app.Logger.Fatal(err)
		}

		options := []orchestrator.Option{
			orchestrator.WithLogger(app.Logger),
			orchestrator.WithListenAddress(app.Config.ListenAddress),
			orchestrator.WithStore(repository),
			orchestrator.WithStreamBroker(streamBroker),
		}

		orc := orchestrator.New(options...)
		go func() {
			orc.Run(cmd.Context())
		}()

		// sit around for term signal
		<-app.TermCh
		app.Logger.Info("got TERM signal, exiting")

	},
}

// install command flags
func init() {
	rootCmd.AddCommand(cmdOrchestrator)
}
