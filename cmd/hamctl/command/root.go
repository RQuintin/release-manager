package command

import (
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// NewCommand returns a new instance of a hamctl command.
func NewCommand() *cobra.Command {
	var client client
	var command = &cobra.Command{
		Use:   "hamctl",
		Short: "hamctl controls a release manager server",
		Run: func(c *cobra.Command, args []string) {
			c.HelpFunc()(c, args)
		},
		PersistentPreRunE: func(c *cobra.Command, args []string) error {
			var err error
			client.authToken, err = envFlag("http-auth-token", "HAMCTL_AUTH_TOKEN", client.authToken)
			if err != nil {
				return err
			}
			return nil
		},
	}
	command.AddCommand(NewPromote(&client))
	command.AddCommand(NewRelease(&client))
	command.AddCommand(NewStatus(&client))
	command.AddCommand(NewPolicy(&client))
	command.PersistentFlags().DurationVar(&client.timeout, "http-timeout", 20*time.Second, "HTTP request timeout")
	command.PersistentFlags().StringVar(&client.baseURL, "http-base-url", "https://release-manager.dev.lunarway.com", "address of the http release manager server")
	command.PersistentFlags().StringVar(&client.authToken, "http-auth-token", "", `auth token for the http service (default environment variable "HAMCTL_AUTH_TOKEN")`)
	return command
}

func envFlag(flagName, env, flagValue string) (string, error) {
	envToken := os.Getenv("HAMCTL_AUTH_TOKEN")
	if flagValue == "" && envToken == "" {
		return "", errors.New(`required flag "http-auth-token" not set"`)
	}
	if flagValue == "" && envToken != "" {
		return envToken, nil
	}
	return flagValue, nil
}
