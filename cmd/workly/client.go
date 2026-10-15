package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/worklydev/workly/internal/client"
)

const defaultURL = "http://localhost:7337"

// addClientFlags adds the connection and output flags shared by the API
// commands (queues, tasks, schedules, runs).
func addClientFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String("url", "", "server URL (env WORKLY_URL, default "+defaultURL+")")
	f.String("api-key", "", "API key (env WORKLY_API_KEY)")
	f.Bool("json", false, "print the API's JSON response")
}

func newClient(cmd *cobra.Command) (*client.ClientWithResponses, error) {
	url, _ := cmd.Flags().GetString("url")
	if url == "" {
		url = os.Getenv("WORKLY_URL")
	}
	if url == "" {
		url = defaultURL
	}
	key, _ := cmd.Flags().GetString("api-key")
	if key == "" {
		key = os.Getenv("WORKLY_API_KEY")
	}
	return client.NewClientWithResponses(strings.TrimRight(url, "/"),
		client.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		client.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("User-Agent", "workly-cli/"+version)
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			return nil
		}))
}

// response is implemented by every generated *XxxResponse.
type response interface{ StatusCode() int }

// check turns a transport error or an error response into a readable error.
func check(res response, err error) error {
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("%w\nIs the server running? Start one with `workly dev`, or set --url / WORKLY_URL", err)
		}
		return err
	}
	status := res.StatusCode()
	if status < 400 {
		return nil
	}
	body := responseBody(res)
	var e client.Error
	if json.Unmarshal(body, &e) != nil || e.Error.Code == "" {
		return fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	if e.Error.Code == "unauthorized" {
		return fmt.Errorf("%s: %s (set --api-key or WORKLY_API_KEY)", e.Error.Code, e.Error.Message)
	}
	return fmt.Errorf("%s: %s", e.Error.Code, e.Error.Message)
}

// responseBody reads the Body field every generated response struct has.
func responseBody(res response) []byte {
	return reflect.ValueOf(res).Elem().FieldByName("Body").Bytes()
}

// output prints the raw JSON body with --json, and calls human otherwise.
func output(cmd *cobra.Command, body []byte, human func(w io.Writer)) error {
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			return err
		}
		buf.WriteByte('\n')
		_, err := buf.WriteTo(w)
		return err
	}
	human(w)
	return nil
}

func table(w io.Writer, header string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if header != "" {
		fmt.Fprintln(tw, header)
	}
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// confirm asks before destructive commands. Without a terminal to ask on,
// it requires --yes rather than guessing.
func confirm(cmd *cobra.Command, question string) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isTerminal(f) {
		return errors.New(question + " Pass --yes to confirm.")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, _ := bufio.NewReader(f).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return errors.New("cancelled")
	}
	return nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// ago formats t relative to now: "3m ago", "in 2h", "now".
func ago(t time.Time) string {
	d := time.Until(t)
	switch {
	case d > time.Second:
		return "in " + short(d)
	case d < -time.Second:
		return short(-d) + " ago"
	}
	return "now"
}

// short formats a duration with its largest unit: 45s, 12m, 3h, 2d.
func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// seconds formats a float of seconds as a Go duration: 1.5 → 1.5s, 3600 → 1h0m0s.
func seconds(s float64) string {
	return time.Duration(s * float64(time.Second)).String()
}

func deref[T any](p *T, none string) string {
	if p == nil {
		return none
	}
	return fmt.Sprint(*p)
}
