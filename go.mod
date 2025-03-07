module github.com/matrix-org/dendrite

replace github.com/nats-io/nats-server/v2 => github.com/neilalexander/nats-server/v2 v2.3.3-0.20220104162330-c76d5fd70423

replace github.com/nats-io/nats.go => github.com/neilalexander/nats.go v1.11.1-0.20220104162523-f4ddebe1061c

require (
	github.com/Arceliar/ironwood v0.0.0-20211125050254-8951369625d0
	github.com/Arceliar/phony v0.0.0-20210209235338-dde1a8dca979
	github.com/DATA-DOG/go-sqlmock v1.5.0
	github.com/HdrHistogram/hdrhistogram-go v1.1.2 // indirect
	github.com/MFAshby/stdemuxerhook v1.0.0
	github.com/Masterminds/semver/v3 v3.1.1
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/codeclysm/extract v2.2.0+incompatible
	github.com/containerd/containerd v1.5.9 // indirect
	github.com/docker/docker v20.10.12+incompatible
	github.com/docker/go-connections v0.4.0
	github.com/frankban/quicktest v1.14.0 // indirect
	github.com/getsentry/sentry-go v0.12.0
	github.com/gologme/log v1.3.0
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/websocket v1.4.2
	github.com/h2non/filetype v1.1.3 // indirect
	github.com/hashicorp/golang-lru v0.5.4
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/juju/testing v0.0.0-20211215003918-77eb13d6cad2 // indirect
	github.com/klauspost/compress v1.14.2 // indirect
	github.com/lib/pq v1.10.4
	github.com/libp2p/go-libp2p v0.13.0
	github.com/libp2p/go-libp2p-circuit v0.4.0
	github.com/libp2p/go-libp2p-core v0.8.3
	github.com/libp2p/go-libp2p-gostream v0.3.1
	github.com/libp2p/go-libp2p-http v0.2.0
	github.com/libp2p/go-libp2p-kad-dht v0.11.1
	github.com/libp2p/go-libp2p-pubsub v0.4.1
	github.com/libp2p/go-libp2p-record v0.1.3
	github.com/lucas-clemente/quic-go v0.22.0
	github.com/marten-seemann/qtls-go1-17 v0.1.0 // indirect
	github.com/matrix-org/dugong v0.0.0-20210921133753-66e6b1c67e2e
	github.com/matrix-org/go-http-js-libp2p v0.0.0-20200518170932-783164aeeda4
	github.com/matrix-org/go-sqlite3-js v0.0.0-20210709140738-b0d1ba599a6d
	github.com/matrix-org/gomatrix v0.0.0-20210324163249-be2af5ef2e16
	github.com/matrix-org/gomatrixserverlib v0.0.0-20220209202448-9805ef634335
	github.com/matrix-org/pinecone v0.0.0-20220121094951-351265543ddf
	github.com/matrix-org/util v0.0.0-20200807132607-55161520e1d4
	github.com/mattn/go-sqlite3 v1.14.10
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/nats-io/nats-server/v2 v2.3.2
	github.com/nats-io/nats.go v1.13.1-0.20211122170419-d7c1d78a50fc
	github.com/neilalexander/utp v0.1.1-0.20210727203401-54ae7b1cd5f9
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646
	github.com/ngrok/sqlmw v0.0.0-20211220175533-9d16fdc47b31
	github.com/opentracing/opentracing-go v1.2.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/pressly/goose v2.7.0+incompatible
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/common v0.32.1 // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	github.com/sirupsen/logrus v1.8.1
	github.com/tidwall/gjson v1.14.0
	github.com/tidwall/sjson v1.2.4
	github.com/uber/jaeger-client-go v2.30.0+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible
	github.com/yggdrasil-network/yggdrasil-go v0.4.2
	go.uber.org/atomic v1.9.0
	golang.org/x/crypto v0.0.0-20220209195652-db638375bc3a
	golang.org/x/image v0.0.0-20211028202545-6944b10bf410
	golang.org/x/mobile v0.0.0-20220112015953-858099ff7816
	golang.org/x/net v0.0.0-20220127200216-cd36cc0744dd
	golang.org/x/sys v0.0.0-20220207234003-57398862261d // indirect
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211
	gopkg.in/h2non/bimg.v1 v1.1.5
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
	nhooyr.io/websocket v1.8.7
)

go 1.16
