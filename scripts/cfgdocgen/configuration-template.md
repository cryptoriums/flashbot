---
description: Telliot tweaks and settings to keep your rig running smoothly.
---

# Configuration reference

## CLI reference

Telliot commands and config file options are as the following:

#### Required Flags <a id="docs-internal-guid-d1a57725-7fff-a753-9236-759dd3f42eed"></a>

* `--config` \(path to your config file.\)

#### Telliot Commands
{{range .CliDocs}}
* `{{ .Name }}`

```
{{ .CmdOutput }}
```
{{end}}
#### .env file options:

{{range .EnvDocs}}
* `{{ .Name }}` {{if .Required }}\(required\){{end}} - {{ .Help }}
{{end}}

#### Config file options:
```json
{{.CfgDocs}}
```
Here are the config defaults in json format:
```json
{{.CfgDefault}}
```
### Log levels
Note the default level is "INFO", so to turn down the number of logs, enter "WARN" or "ERROR".

DEBUG - logs everything in INFO and additional developer logs

INFO - logs most information about the reporting operation

WARN - logs all warnings and errors

ERROR - logs only serious errors
