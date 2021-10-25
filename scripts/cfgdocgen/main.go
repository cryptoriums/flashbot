// Copyright (c) The Cryptorium Authors.
// Licensed under the MIT License.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"text/template"

	"github.com/alecthomas/kong"
	"github.com/cryptoriums/telliot/pkg/cli"
	"github.com/cryptoriums/telliot/pkg/config"
	"github.com/fatih/camelcase"
	"github.com/fatih/structtag"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
)

type cliOutput struct {
	Name      string
	CmdOutput string
}

type cfgDoc struct {
	Name     string
	Help     string
	Default  interface{}
	Required bool
}

func (c *cfgDoc) String() string {
	d := fmt.Sprintf("Required:%v, Default:%v", c.Required, c.Default)
	if c.Help != "" {
		d += fmt.Sprintf(", Description:%s", c.Help)

	}
	return d
}

type envDoc struct {
	Name     string
	Help     string
	Required bool
}

func main() {
	ctx := kong.Parse(&CLI, kong.Name("cfgdocgen"),
		kong.Description("Config docs generator tool"),
		kong.UsageOnError())

	ctx.FatalIfErrorf(ctx.Run(*ctx))
}

var CLI struct {
	Generate GenerateCmd `cmd:"" help:"Generate docs."`
}

type GenerateCmd struct {
	CliBin string `arg:"" required:"" name:"cli-bin" help:"Cli binary for generating command outputs." type:"path"`
	Output string `arg:"" optional:"" name:"output" help:"Output file for the generated doc." type:"path"`
}

func (l *GenerateCmd) Run() error {
	var err error
	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))

	// Generating cli docs from the cli struct.
	cliDocsMap := make(map[string]string)
	cli := &cli.CLIDefault
	if err = NewCliDocsGenerator(logger, l.CliBin).genCliDocs("", reflect.ValueOf(cli).Elem(), cliDocsMap); err != nil {
		level.Error(logger).Log("msg", "failed to generate", "type", "cli docs", "err", err)
		return err
	}
	cliDocs := make([]cliOutput, 0)
	keys := []string{}
	for k := range cliDocsMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cliDocs = append(cliDocs, cliOutput{
			Name:      k,
			CmdOutput: cliDocsMap[k],
		})
	}
	// Generating env docs from the .env.example file.
	var (
		envDocs []envDoc
	)
	if envDocs, err = genEnvDocs(); err != nil {
		level.Error(logger).Log("msg", "failed to generate", "type", "env docs", "err", err)
		return err
	}

	// Generating config docs from the default config object.
	cfgDocsMap := make(map[string]interface{})
	cfg := config.DefaultConfig
	if err := genCfgDocs(reflect.ValueOf(cfg), cfgDocsMap); err != nil {
		level.Error(logger).Log("msg", "failed to generate", "type", "cli", "err", err)
		return err
	}
	// Converto to json
	cfgDocs, err := json.MarshalIndent(cfgDocsMap, "", "\t")
	if err != nil {
		level.Error(logger).Log("msg", "marshaling config docs to json", "err", err)
		return err
	}
	defCfg, err := json.MarshalIndent(config.DefaultConfig, "", "\t")
	if err != nil {
		level.Error(logger).Log("msg", "marshaling default config to json", "err", err)
		return err
	}

	// Sort json keys.
	defCfg, err = JsonRemarshal(defCfg)
	if err != nil {
		level.Error(logger).Log("msg", "sorting default config json", "err", err)
		return err
	}

	tmpl := template.Must(template.ParseFiles("scripts/cfgdocgen/configuration-template.md"))
	outf, err := os.Create(l.Output)
	if err != nil {
		level.Error(logger).Log("msg", "failed to open output file, redirecting to stdout", "err", err, "output", l.Output)
		outf = os.Stdout
	}
	err = tmpl.Execute(outf,
		struct {
			CliDocs    []cliOutput
			EnvDocs    []envDoc
			CfgDocs    string
			CfgDefault string
		}{
			CliDocs:    cliDocs,
			EnvDocs:    envDocs,
			CfgDocs:    string(cfgDocs),
			CfgDefault: string(defCfg),
		})
	if err != nil {
		level.Error(logger).Log("msg", "failed to execute template", "err", err)
		return err
	}
	logger.Log("msg", "success")
	return nil
}

func NewCliDocsGenerator(logger log.Logger, cliBin string) *cliDocsGenerator {
	return &cliDocsGenerator{logger, cliBin}
}

type cliDocsGenerator struct {
	logger log.Logger
	cliBin string
}

func (self *cliDocsGenerator) cmdOutput(args string) string {
	_args := strings.Split(args, " ")
	_args = append(_args, "--help")
	cmd := exec.Command(self.cliBin, _args...)
	stdout, err := cmd.Output()
	if err != nil {
		level.Error(self.logger).Log("msg", "failed to execute telliot command", "err", err, "args", args, "cli", self.cliBin)
		os.Exit(1)
	}
	return string(stdout)

}

func (self *cliDocsGenerator) genCliDocs(parent string, cli reflect.Value, docs map[string]string) error {
	for i := 0; i < cli.NumField(); i++ {
		v := cli.Field(i)
		t := cli.Type().Field(i)
		switch v.Kind() {
		case reflect.Struct:

			// If there is no child struct fields then v is a leaf.
			leafFound := true
			if v.Type().NumField() > 0 {
				// Checking the first field to know if it's a leaf.
				v0 := v.Type().Field(0)

				tags, err := structtag.Parse(string(v0.Tag))
				if err != nil {
					return errors.Wrapf(err, "%s: failed to parse tag %q", v.Type().Field(i).Name, v.Type().Field(i).Tag)
				}
				_, err = tags.Get("cmd")
				leafFound = err != nil
			}

			if leafFound {
				// v is a leaf in the cmd tree.
				cmdName := strings.ToLower(strings.Join(camelcase.Split(t.Name), "-"))
				if len(parent) > 0 {
					cmdName = fmt.Sprintf("%s %s", parent, cmdName)
				}

				// Can skip non commands tags.
				_, ok := t.Tag.Lookup("cmd")
				if !ok {
					continue
				}

				docs[cmdName] = self.cmdOutput(cmdName)
			} else {
				parentName := strings.ToLower(t.Name)
				if len(parent) > 0 {
					parentName = fmt.Sprintf("%s %s", parent, parentName)
				}
				// Add top level command too.
				docs[parentName] = self.cmdOutput(parentName)
				if err := self.genCliDocs(parentName, v, docs); err != nil {
					return errors.Wrapf(err, "%s", t.Name)
				}
			}

		case reflect.Ptr:
			return errors.New("nil pointers are not allowed in configuration")
		case reflect.Interface:

		}
	}
	return nil
}

func genCfgDocs(cfg reflect.Value, cfgDocs map[string]interface{}) error {
	for i := 0; i < cfg.NumField(); i++ {
		v := cfg.Field(i)
		t := cfg.Type().Field(i)
		switch v.Kind() {
		case reflect.Struct:
			cfgDocs[t.Name] = make(map[string]interface{})
			if err := genCfgDocs(v, (cfgDocs[t.Name]).(map[string]interface{})); err != nil {
				return err
			}
		default:
			name := t.Name
			doc := cfgDoc{
				Name:    name,
				Default: v.Interface(),
			}
			tags, _ := structtag.Parse(string(t.Tag))
			if tags != nil {
				help, _ := tags.Get("help")
				if help != nil {
					doc.Help = help.Value()
				}

				// Respect the json name if present.
				jsonName, _ := tags.Get("json")
				if jsonName != nil {
					name = jsonName.Value()
					doc.Name = name
				}
			}
			cfgDocs[name] = doc.String()
		}
	}
	return nil
}

func genEnvDocs() ([]envDoc, error) {
	docs := make([]envDoc, 0)
	bytes, err := ioutil.ReadFile("configs/.env.example")
	if err != nil {
		return nil, err
	}
	envExamples := strings.Split(string(bytes), "\n")
	for _, env := range envExamples {
		var (
			help     string
			required bool
		)

		if env == "" { // Skip empty lines.
			continue
		}

		comment := strings.TrimSpace(strings.Split(env, "#")[1])
		help = comment

		parts := strings.Fields(comment)
		if len(parts) > 0 && parts[0] == "required" {
			required = true
			help = strings.TrimSpace(strings.TrimPrefix(comment, "required"))
		}

		name := strings.TrimSpace(strings.Split(env, "=")[0])
		docs = append(docs, envDoc{
			Name:     name,
			Help:     help,
			Required: required,
		})
	}
	return docs, nil
}

func JsonRemarshal(bytes []byte) ([]byte, error) {
	var ifce interface{}
	err := json.Unmarshal(bytes, &ifce)
	if err != nil {
		return []byte{}, err
	}
	output, err := json.MarshalIndent(ifce, "", "\t")
	if err != nil {
		return []byte{}, err
	}
	return output, nil
}
