package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/scanbench"
	"github.com/PorticoMediaServer/portico/server/internal/store/scan"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if err := storage.Helper(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type report struct {
	Label              string               `json:"label"`
	Mode               string               `json:"mode"`
	RequestedStorage   string               `json:"requested_storage"`
	Fixture            scanbench.Fixture    `json:"fixture"`
	DeclaredSourceSHA  string               `json:"declared_source_sha"`
	SchemaSHA          string               `json:"schema_sha"`
	Engine             *string              `json:"engine"` // no SQL engine is executed by this slice
	Counts             scan.TraversalCounts `json:"observed"`
	TraversalCompleted bool                 `json:"traversal_completed"`
	TraversalNS        int64                `json:"traversal_ns"`
	FilenameVisibleNS  *int64               `json:"filename_visible_ns"`
	BasicNS            *int64               `json:"basic_ns"`
	Unavailable        []string             `json:"unavailable"`
	Error              string               `json:"error,omitempty"`
	HostBefore         qual.HostEvidence    `json:"host_before"`
	HostAfter          qual.HostEvidence    `json:"host_after"`
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("scanbench", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.String("mode", "traverse", "generate or traverse")
	root := flags.String("root", "", "new fixture root, or existing generated root")
	template := flags.String("template", "", "real media seed (generate only)")
	files := flags.Int("files", 1000, "physical files; 400000 only on QL-scheduled scale runs")
	seed := flags.Uint64("seed", 1, "deterministic filename seed")
	scale := flags.Bool("scheduled-scale", false, "QL scheduled hosted/reference scale run; never mini-1")
	source := flags.String("source-sha", "", "declared source SHA for evidence")
	media := flags.String("storage", "ssd", "requested media profile: ssd or throttled-hdd; actual limits are telemetry")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *root == "" || (*media != "ssd" && *media != "throttled-hdd") || (*mode != "generate" && *mode != "traverse") {
		return errors.New("invalid mode/root/storage")
	}
	if *mode == "generate" {
		if err := allowCount(*files, *scale); err != nil {
			return err
		}
		fixture, err := scanbench.Generate(ctx, *root, scanbench.FixtureConfig{Files: *files, Seed: *seed, Template: *template})
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Label   string            `json:"label"`
			Mode    string            `json:"mode"`
			Fixture scanbench.Fixture `json:"fixture"`
		}{"DIRECTIONAL", "generation-only", fixture})
	}
	if runtime.GOOS != "linux" {
		return errors.New("FIRST native-cookie traversal harness requires Linux")
	}
	if len(*source) != 40 || !hexSHA(*source) {
		return errors.New("traverse requires -source-sha with 40 hex characters")
	}
	fixture, err := scanbench.ReadFixture(*root)
	if err != nil {
		return err
	}
	if err := allowCount(fixture.Files, *scale); err != nil {
		return err
	}
	r := report{Label: "DIRECTIONAL", Mode: "traversal-only", RequestedStorage: *media, Fixture: fixture, DeclaredSourceSHA: *source, SchemaSHA: schema.SHA256,
		Unavailable: []string{"InventoryAccept fresh constructor/body and directory/EOF protocol", "Basic accepting/probe driver", "physical DeviceIO broker; whole-helper custody remains required"}, HostBefore: qual.CaptureHost()}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	client := storage.New(binary)
	scope := storage.NewOperationScope()
	client.SourceOperations = scope
	start := time.Now()
	r.Counts, err = scan.WalkInventory(ctx, client, "schema-v1-scanbench", *root, 256<<10, func(_ context.Context, _ scan.InventoryTurn) error { return nil })
	// Cancellation is not physical retirement. Join this owner's exact work,
	// including failures, before stopping the traversal clock and reporting.
	scope.Close()
	joinErr := scope.Wait(context.Background())
	r.TraversalNS = time.Since(start).Nanoseconds()
	err = errors.Join(err, joinErr)
	if err == nil && r.Counts.Files != uint64(fixture.Files) {
		err = fmt.Errorf("observed %d files, fixture declares %d", r.Counts.Files, fixture.Files)
	}
	r.TraversalCompleted = err == nil
	if err != nil {
		r.Error = err.Error()
	}
	r.HostAfter = qual.CaptureHost()
	return errors.Join(err, json.NewEncoder(out).Encode(r))
}

func allowCount(files int, scheduled bool) error {
	if files < 1 || files > scanbench.QualificationFiles {
		return errors.New("fixture count outside 1..400000")
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if files >= 100000 && (!scheduled || host == "ehler-homelab-mini-1" || host == "ehler-personal" || host == "mini-1") {
		return errors.New("100k+ fixtures require QL scheduled scale execution away from mini-1")
	}
	return nil
}
func hexSHA(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
