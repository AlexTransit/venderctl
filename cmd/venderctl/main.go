package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"os"
	"os/signal"
	"strings"
	"syscall"

	log2 "github.com/AlexTransit/vender/log2"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	cmd_control "github.com/AlexTransit/venderctl/cmd/venderctl/control"
	cmd_passwd "github.com/AlexTransit/venderctl/cmd/venderctl/passwd"
	cmd_sponge "github.com/AlexTransit/venderctl/cmd/venderctl/sponge"
	cmd_tax "github.com/AlexTransit/venderctl/cmd/venderctl/tax"

	// cmd_tele "github.com/AlexTransit/venderctl/cmd/venderctl/tele"
	cmd_telegram "github.com/AlexTransit/venderctl/cmd/venderctl/telegram"
	"github.com/juju/errors"

	// _ "golang.org/x/tools/cmd/stringer"

	state_new "github.com/AlexTransit/venderctl/internal/state/new"
	"github.com/AlexTransit/venderctl/internal/tele"
)

var log = log2.NewStderr(log2.LOG_DEBUG)
var commands = []cli.Cmd{
	cmd_control.Cmd,
	cmd_passwd.Cmd,
	cmd_tax.Cmd,
	// cmd_tele.Cmd,
	cmd_sponge.Cmd,
	cmd_telegram.Cmd,
	{Name: "version", Action: versionMain},
}

var BuildVersion string = "unknown" // set by ldflags -X

func main() {
	log.SetFlags(0)

	flags := flag.NewFlagSet("venderctl", flag.ContinueOnError)
	flags.String("config", "venderctl.hcl", "")
	flags.Usage = func() {
		usage := "Usage: venderctl [global options] command [options]\n"
		usage += "\nGlobal options:\n"
		fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
		cmds := "\nCommands:\n"
		for i, c := range commands {
			cmds += fmt.Sprintf("  %s\t%s\n", c.Name, c.Desc)
			if c.Usage != "" {
				cmds += fmt.Sprintf("  %s\t%s\n", strings.Repeat(" ", len(c.Name)), c.Usage)
			}
			if i != len(commands)-1 {
				cmds += "\n"
			}
		}
		fmt.Fprint(flags.Output(), cmds)
	}

	err := flags.Parse(os.Args[1:])
	if err != nil {
		if err == flag.ErrHelp { // usage is already printed
			os.Exit(0)
		}
		log.Fatal(errors.ErrorStack(err))
	}

	cmdName := flags.Arg(0)
	if cmdName == "" {
		flags.Usage()
		os.Exit(1)
	}
	if cli.IsHelp(cmdName) {
		flags.Usage()
		os.Exit(0)
	}
	// log.Printf("command='%s'", cmdName)
	for _, c := range commands {
		if c.Name == cmdName {
			flags.Usage = func() {
				usage := fmt.Sprintf("Usage: venderctl [global options] %s %s\n", cmdName, c.Usage)
				usage += "\nGlobal options:\n"
				fmt.Fprint(flags.Output(), usage)
				flags.PrintDefaults()
			}
			if cli.IsHelp(flags.Arg(2)) {
				flags.Usage()
				os.Exit(0)
			}

			ctxN, g := state_new.NewContext(cmdName, log, tele.NewTele())
			ctx, cancelCTX := context.WithCancel(ctxN)

			// working term signal
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT)
			go func() {
				si := <-sigs
				log.WarningF("income OS signal:%v \n", si)
				log.Infof("stoping %v", c.Name)
				g.Alive.Stop()
				go func() {
					time.Sleep(5 * time.Second)
					g.Log.Infof("venderctl stoped. by timeout")
					os.Exit(0)
				}()

				g.Tele.Close()
				g.Alive.Wait()
				cancelCTX()

				os.Exit(0)
			}()

			g.BuildVersion = BuildVersion

			if cli.SdNotify("start " + cmdName) {
				// under systemd assume systemd journal logging, no timestamp
				log.SetFlags(log2.LServiceFlags)
				log.LogToSyslog("vmc-" + cmdName)
			} else {
				log.SetFlags(log2.LInteractiveFlags)
				log.LogToConsole()
			}
			if c.Name != "version" {
				// Sad difference with vender code: config is read inside cmd.Action, not available here
				// Options considered:
				// - avoid config to environ
				// - duplicate pprofStart code in actions
				// - cmd.PreAction hook -- maybe best in the long run, needed quick
				// not used				g.Error(pprofStart(g, os.Getenv("pprof_listen")))
				log.WarningF("venderctl version=%s starting %s", BuildVersion, cmdName)
			}

			err := c.Action(ctx, flags)
			if err != nil {
				log.Fatalf("%s\nTrace:\n%s", err.Error(), errors.ErrorStack(err))
			}
			os.Exit(0) // success path

		}
	}
	// unknown command
	log.Errorf("unknown command=%s", cmdName)
	flags.Usage()
	os.Exit(1)
}

func versionMain(ctx context.Context, flags *flag.FlagSet) error {
	fmt.Printf("venderctl %s\n", BuildVersion)
	return nil
}
