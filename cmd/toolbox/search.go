package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/solidarity-ai/toolbox/registry"
)

type searchCmd struct {
	Tools    bool   `help:"Search for individual tools instead of packages."`
	Packages bool   `help:"Search for packages (default)."`
	JSON     bool   `help:"Emit structured JSON output."`
	Runtime  string `help:"Filter results by runtime."`
	Effect   string `help:"Filter results by effect."`
	Limit    int    `default:"20" help:"Maximum number of results."`
	Offset   int    `default:"0" help:"Result offset for pagination."`
	Query    string `arg:"" name:"query" help:"Search query."`
}

func newToolRegistrySearchClient() (*registry.ToolRegistrySearchClient, error) {
	baseURL, enabled, err := toolRegistryConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("search: registry is disabled")
	}
	return registry.NewToolRegistrySearchClient(baseURL, newToolRegistryHTTPClient())
}

func runSearch(cmd searchCmd, stdout io.Writer) error {
	if cmd.Tools && cmd.Packages {
		return fmt.Errorf("search: --tools and --packages are mutually exclusive")
	}
	if strings.TrimSpace(cmd.Query) == "" {
		return fmt.Errorf("search: query must not be empty")
	}
	if cmd.Limit <= 0 {
		return fmt.Errorf("search: --limit must be greater than zero")
	}
	if cmd.Offset < 0 {
		return fmt.Errorf("search: --offset must be zero or greater")
	}

	client, err := newToolRegistrySearchClient()
	if err != nil {
		return err
	}

	query := registry.SearchQuery{
		Q:       strings.TrimSpace(cmd.Query),
		Runtime: strings.TrimSpace(cmd.Runtime),
		Effect:  strings.TrimSpace(cmd.Effect),
		Limit:   cmd.Limit,
		Offset:  cmd.Offset,
	}

	ctx := context.Background()
	if cmd.Tools {
		response, err := client.SearchTools(ctx, query)
		if err != nil {
			return err
		}
		if cmd.JSON {
			return writeJSON(stdout, response)
		}
		return writeToolSearchResults(stdout, response.Hits)
	}

	response, err := client.SearchPackages(ctx, query)
	if err != nil {
		return err
	}
	if cmd.JSON {
		return writeJSON(stdout, response)
	}
	return writePackageSearchResults(stdout, response.Hits)
}

func writeJSON(stdout io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", data)
	return err
}

func writePackageSearchResults(stdout io.Writer, hits []registry.PackageSearchHit) error {
	if len(hits) == 0 {
		_, err := fmt.Fprintln(stdout, "no search results")
		return err
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODULE\tLATEST\tRUNTIME\tNAME")
	for _, hit := range hits {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", hit.ModulePath, valueOrDash(hit.LatestVersion), valueOrDash(hit.Runtime), valueOrDash(hit.Name))
		if desc := strings.TrimSpace(hit.Description); desc != "" {
			fmt.Fprintf(tw, "  %s\t\t\t\n", desc)
		}
	}
	return tw.Flush()
}

func writeToolSearchResults(stdout io.Writer, hits []registry.ToolSearchHit) error {
	if len(hits) == 0 {
		_, err := fmt.Fprintln(stdout, "no search results")
		return err
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODULE\tLATEST\tTOOL\tEFFECT\tPACKAGE")
	for _, hit := range hits {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", hit.ModulePath, valueOrDash(hit.LatestVersion), valueOrDash(hit.ToolPath), valueOrDash(hit.Effect), valueOrDash(hit.PackageName))
		if desc := strings.TrimSpace(hit.Description); desc != "" {
			fmt.Fprintf(tw, "  %s\t\t\t\t\n", desc)
		}
	}
	return tw.Flush()
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
