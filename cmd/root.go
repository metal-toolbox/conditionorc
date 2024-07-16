/*
Copyright © 2022 Metal toolbox authors <>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/metal-toolbox/conditionorc/internal/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.hollow.sh/toolbox/ginjwt"
)

var (
	logLevel string
	cfgFile  string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "conditionorc",
	Short:   "server condition orchestrator",
	Version: version.Current().String(),
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	// Read in env vars with appName as prefix
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "conditionorc.yaml", "default is ./conditionorc.yaml")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "set logging level - debug, trace")

	rootCmd.PersistentFlags().Bool("oidc", true, "Use OIDC Auth for API endpoints")
	ginjwt.BindFlagFromViperInst(viper.GetViper(), "oidc.enabled", rootCmd.PersistentFlags().Lookup("oidc"))
}
