module github.com/arloliu/mebo/tests/measurev2

go 1.25.0

require (
	github.com/arloliu/mebo v1.1.0
	github.com/stretchr/testify v1.11.1
	golang.org/x/sys v0.47.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/klauspost/compress v1.19.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/arloliu/mebo => ../..
