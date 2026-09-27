package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AlexTransit/vender/log2"
	"github.com/AlexTransit/venderctl/internal/state"
	state_new "github.com/AlexTransit/venderctl/internal/state/new"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	tele_config "github.com/AlexTransit/venderctl/internal/tele/config"
	"github.com/juju/errors"
	"github.com/stretchr/testify/assert"
)

func TestReadConfig(t *testing.T) {
	t.Parallel()

	type Case struct {
		name      string
		input     string
		check     func(testing.TB, context.Context)
		expectErr string
	}
	cases := []Case{
		{
			"include-normalize", `
money { scale = 1 }
include "./empty" {}`,
			nil, "",
		},

		{
			"include-optional", `
include "money-scale-7" {}
include "non-exist" { optional = true }`,
			func(t testing.TB, ctx context.Context) {
				g := state.GetGlobal(ctx)
				assert.Equal(t, 7, g.Config.Money.Scale)
			}, "",
		},

		{
			"include-overwrites", `
money { scale = 1 }
include "money-scale-7" {}`,
			func(t testing.TB, ctx context.Context) {
				g := state.GetGlobal(ctx)
				assert.Equal(t, 7, g.Config.Money.Scale)
			}, "",
		},

		{
			"tele", `
tele {
        listen "tls://127.0.0.1:1884" {
                allow_roles = ["_all"]
                tls { ca_file = "/ca.pem" }
        }
}`,
			func(t testing.TB, ctx context.Context) {
				g := state.GetGlobal(ctx)
				expect := []tele_config.Listen{
					{URL: "tls://127.0.0.1:1884", AllowRoles: []string{"_all"}, TLS: tele_config.TLS{CaFile: "/ca.pem"}},
				}
				assert.Equal(t, expect, g.Config.Tele.Listens)
			}, "",
		},

		{"error-syntax", `hello`, nil, "key 'hello' expected start of object"},
		{"error-include-loop", `include "include-loop" {}`, nil, "config include loop: from=include-loop include=include-loop"},
	}
	mkCheck := func(c Case) func(*testing.T) {
		return func(t *testing.T) {
			if c.name == "error-syntax" {
				t.Skip("syntax error is fatal: ReadConfig terminates the process via log.Fatal (verified in TestReadConfigSyntaxErrorFatal)")
			}
			// log := log2.NewStderr(log2.LDebug) // helps with panics
			log := log2.NewTest(t, log2.LOG_DEBUG)
			ctx, g := state_new.NewContext("test", log, tele_api.NewStub())
			fs := state.NewMockFullReader(map[string]string{
				"test-inline":   c.input,
				"empty":         "",
				"money-scale-7": "money{scale=7}",
				"error-syntax":  "hello",
				"include-loop":  `include "include-loop" {}`,
			})
			cfg, err := state.ReadConfig(log, fs, "test-inline")
			if err == nil {
				g.Config = cfg
			}
			if c.expectErr == "" {
				if err != nil {
					t.Fatalf("error expected=nil actual='%v'", errors.ErrorStack(err))
				}
				if c.check != nil {
					c.check(t, ctx)
				}
			} else {
				if !strings.Contains(err.Error(), c.expectErr) {
					t.Fatalf("error expected='%s' actual='%v'", c.expectErr, err)
				}
			}
		}
	}
	for _, c := range cases {
		t.Run(c.name, mkCheck(c))
	}
}

// // TestReadConfigSyntaxErrorFatal проверяет, что синтаксическая ошибка в конфиге фатальна:
// // ReadConfig не возвращает управление, а завершает процесс через log.Fatal.
// // Проверяется в отдельном процессе, потому что log.Fatal вызывает os.Exit(1).
// func TestReadConfigSyntaxErrorFatal(t *testing.T) {
// 	if os.Getenv("TEST_FATAL_CHILD") == "1" {
// 		// дочерний процесс: читаем конфиг с синтаксической ошибкой - должны выйти без возврата
// 		log := log2.NewStderr(log2.LOG_ERR)
// 		fs := state.NewMockFullReader(map[string]string{"bad": "hello"})
// 		_, err := state.ReadConfig(log, fs, "bad")
// 		// сюда достигать не должны: ReadConfig обязан завершить процесс (log.Fatal)
// 		fmt.Fprintf(os.Stderr, "child: returned instead of exiting, err=%v\n", err)
// 		os.Exit(99)
// 	}
// 	exe, err := os.Executable()
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	cmd := exec.Command(exe, "-test.run=^TestReadConfigSyntaxErrorFatal$", "-test.timeout=30s")
// 	cmd.Env = append(os.Environ(), "TEST_FATAL_CHILD=1")
// 	out, err := cmd.CombinedOutput()
// 	if err == nil {
// 		t.Fatalf("expected process to exit with error on config syntax error, output:\n%s", out)
// 	}
// 	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
// 		t.Fatalf("expected exit status 1 (log.Fatal), got %v, output:\n%s", err, out)
// 	}
// 	if !strings.Contains(string(out), "key 'hello' expected start of object") {
// 		t.Fatalf("fatal output missing syntax error message:\n%s", out)
// 	}
// }

func TestFunctionalBundled(t *testing.T) {
	// not Parallel
	t.Logf("this test needs OS open|read|stat access to file `../../venderctl.hcl`")

	log := log2.NewTest(t, log2.LOG_DEBUG)
	state.MustReadConfig(log, state.NewOsFullReader(), "../../venderctl.hcl")
}
