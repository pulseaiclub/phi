module github.com/pulseaiclub/phi

go 1.26.3

require (
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/pulseaiclub/phi/ext v0.19.0
	github.com/pulseaiclub/pli v0.1.0
	github.com/pulseaiclub/xui v0.1.5
	github.com/stretchr/testify v1.12.1
	github.com/yuin/goldmark v1.8.5
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/dlclark/regexp2/v2 v2.2.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace github.com/pulseaiclub/phi/ext => ./ext/go
