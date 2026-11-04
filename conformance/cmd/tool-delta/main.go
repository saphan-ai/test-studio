// Command tool-delta prints the new SARIF results in a scanner report, after
// applying the repository waiver file. It never prints the finding message.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	dateLayout = "2006-01-02"
	exitOK     = 0
	exitNew    = 1
	exitUsage  = 2

	usageLine  = "usage: tool-delta --current <sarif> [--baseline <sarif>] [--waivers <.saphan-waivers.yaml>] [--today <YYYY-MM-DD>]"
	remarkLine = "remark: identity by derived fingerprint shifts if the rule's message or the file path changes."
)

// errPrinted marks a usage failure whose text is already on out.
var errPrinted = errors.New("usage already printed")

type options struct {
	current  string
	baseline string
	waivers  string
	today    string
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// run returns 0 when no NEW line was printed, 1 when at least one was, and 2
// for a usage or input error. The remark is the last line on every path.
func run(args []string, out io.Writer) int {
	opts, err := parseArgs(args, out)
	if err != nil {
		return finishErr(out, err)
	}
	current, err := loadFindings(opts.current)
	if err != nil {
		return finishErr(out, err)
	}
	var base []finding
	if opts.baseline != "" {
		base, err = loadFindings(opts.baseline)
		if err != nil {
			return finishErr(out, err)
		}
	}
	var waivers []waiver
	if opts.waivers != "" {
		waivers, err = loadWaivers(opts.waivers)
		if err != nil {
			return finishErr(out, err)
		}
	}
	today, err := time.ParseInLocation(dateLayout, opts.today, time.UTC)
	if err != nil {
		return finishErr(out, fmt.Errorf("--today %q is not a UTC date YYYY-MM-DD", opts.today))
	}
	printed := diff(current, base, waivers, today)
	writeDelta(out, printed)
	if len(printed.neu) > 0 {
		return exitNew
	}
	return exitOK
}

func parseArgs(args []string, out io.Writer) (options, error) {
	fs := flag.NewFlagSet("tool-delta", flag.ContinueOnError)
	fs.SetOutput(out)
	current := fs.String("current", "", "SARIF report to judge")
	baseline := fs.String("baseline", "", "SARIF report of already-known results")
	waivers := fs.String("waivers", "", "waiver file (.saphan-waivers.yaml)")
	today := fs.String("today", time.Now().UTC().Format(dateLayout), "UTC date YYYY-MM-DD the expiry is judged against")
	if err := fs.Parse(args); err != nil {
		return options{}, errPrinted
	}
	if *current == "" || fs.NArg() > 0 {
		fmt.Fprintln(out, usageLine)
		return options{}, errPrinted
	}
	return options{current: *current, baseline: *baseline, waivers: *waivers, today: *today}, nil
}

func finishErr(out io.Writer, err error) int {
	if err != nil && !errors.Is(err, errPrinted) {
		fmt.Fprintln(out, err.Error())
	}
	fmt.Fprintln(out, remarkLine)
	return exitUsage
}
