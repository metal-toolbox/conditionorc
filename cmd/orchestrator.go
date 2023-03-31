package cmd

import (
	"context"
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

		// setup cancel context with cancel func
		ctx, cancelFunc := context.WithCancel(cmd.Context())

		// routine listens for termination signal and cancels the context
		go func() {
			<-app.TermCh
			app.Logger.Info("got TERM signal, exiting...")
			cancelFunc()
		}()

		repository, err := store.NewStore(ctx, app.Config, app.Config.ConditionDefinitions, app.Logger)
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
		orc.Run(ctx)
	},
}

// install command flags
func init() {
	rootCmd.AddCommand(cmdOrchestrator)
}
