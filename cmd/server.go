package cmd

import (
	"doc-converter/server"

	"github.com/spf13/cobra"
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Starts the doc-converter web server",
	Long:  `Starts the web server that provides a WebSocket API for document conversion.`,
	Run: func(cmd *cobra.Command, args []string) {
		server.Run()
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
}
