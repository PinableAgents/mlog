module github.com/PinableAgents/mlog/benchmarks

go 1.24

require (
	github.com/PinableAgents/mlog v0.0.0
	github.com/ai-mmo/lumberjack v0.0.5
	github.com/rs/zerolog v1.34.0
	github.com/sirupsen/logrus v1.9.4
	go.uber.org/zap v1.27.1
)

require (
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.13.0 // indirect
)

replace github.com/PinableAgents/mlog => ..
