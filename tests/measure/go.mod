module github.com/arloliu/mebo/tests/measure

go 1.24.0

require github.com/arloliu/mebo v1.1.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/valyala/gozstd v1.23.2 // indirect
)

replace github.com/arloliu/mebo => ../..
