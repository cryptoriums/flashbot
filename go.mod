module github.com/cryptoriums/flashbot

go 1.16

require (
	github.com/cryptoriums/telliot v0.3.1-0.20210913212949-f632a0da756a
	github.com/ethereum/go-ethereum v1.10.8
	github.com/go-kit/kit v0.10.0
	github.com/go-kit/log v0.1.0
	github.com/joho/godotenv v1.3.0
	github.com/pkg/errors v0.9.1
	golang.org/x/tools v0.1.5
)

replace github.com/tellor-io/telliot => ../../tellor-io/telliot
