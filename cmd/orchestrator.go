package cmd

import (
	"context"
	"log"

	"github.com/equinix-labs/otel-init-go/otelinit"

	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/orchestrator"
	"github.com/metal-toolbox/conditionorc/internal/orchestrator/notify"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/spf13/cobra"
	"go.hollow.sh/toolbox/events"
)

var (
	replicaCount int
	facility     string
)

// install orchestrator command
var cmdOrchestrator = &cobra.Command{
	Use:   "orchestrator",
	Short: "Run condition orchestrator service",
	Run: func(cmd *cobra.Command, args []string) {
		app, termCh, err := app.New(cmd.Context(), model.AppKindOrchestrator, cfgFile, model.LogLevel(logLevel))
		if err != nil {
			log.Fatal(err)
		}

		_, otelShutdown := otelinit.InitOpenTelemetry(cmd.Context(), "conditionorc-orchestrator")
		defer otelShutdown(cmd.Context())

		// serve metrics
		metrics.ListenAndServe()

		// setup cancel context with cancel func
		ctx, cancelFunc := context.WithCancel(cmd.Context())

		// routine listens for termination signal and cancels the context
		go func() {
			<-termCh
			app.Logger.Info("got TERM signal, exiting...")
			cancelFunc()
		}()

		streamBroker, err := events.NewStream(app.Config.NatsOptions)
		if err != nil {
			app.Logger.Fatal(err)
		}

		if err := streamBroker.Open(); err != nil {
			app.Logger.Fatal(err)
		}

		repository, err := store.NewStore(ctx, app.Config, app.Config.ConditionDefinitions, app.Logger, streamBroker)
		if err != nil {
			app.Logger.Fatal(err)
		}

		notifier := notify.New(app.Logger, app.Config.Notifications)

		options := []orchestrator.Option{
			orchestrator.WithLogger(app.Logger),
			orchestrator.WithListenAddress(app.Config.ListenAddress),
			orchestrator.WithStore(repository),
			orchestrator.WithStreamBroker(streamBroker),
			orchestrator.WithNotifier(notifier),
			orchestrator.WithFacility(facility),
			orchestrator.WithConditionDefs(app.Config.ConditionDefinitions),
		}

		app.Logger.Info("configuring status KV support")
		options = append(options, orchestrator.WithReplicas(replicaCount))

		orc := orchestrator.New(options...)
		orc.Run(ctx)
	},
}

// install command flags
func init() {
	pflags := cmdOrchestrator.PersistentFlags()
	pflags.IntVarP(&replicaCount, "replica-count", "r", 3,
		"the number of replicas to configure for the NATS status KV store")

	pflags.StringVarP(&facility, "facility", "f", "", "a site-specific token to focus this orchestrator's activities")

	if err := cmdOrchestrator.MarkPersistentFlagRequired("facility"); err != nil {
		log.Fatal("marking facility as required:", err)
	}

	rootCmd.AddCommand(cmdOrchestrator)
}
