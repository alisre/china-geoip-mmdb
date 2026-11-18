BIN        := china-geoip-mmdb
CONFIG     ?= config.yaml
DATA_DIR   := data
# Prefix for github.com / raw.githubusercontent.com URLs; GH_PROXY= disables it.
GH_PROXY   ?= https://gh-proxy.com/
XDB_TAG    ?= latest
ifeq ($(XDB_TAG),latest)
XDB_BASE   := $(GH_PROXY)https://github.com/fa1seut0pia/ip2region-xdb/releases/latest/download
else
XDB_BASE   := $(GH_PROXY)https://github.com/fa1seut0pia/ip2region-xdb/releases/download/$(XDB_TAG)
endif
ASN_URL    ?= $(GH_PROXY)https://raw.githubusercontent.com/Loyalsoldier/geoip/refs/heads/release/GeoLite2-ASN.mmdb
APNIC_URL  ?= https://ftp.apnic.net/stats/apnic/delegated-apnic-latest
COIP_BASE  ?= $(GH_PROXY)https://raw.githubusercontent.com/gaoyifan/china-operator-ip/ip-lists
IPTOASN_BASE ?= https://iptoasn.com/data
# DB-IP publishes monthly; the previous month is used if this month is not out yet.
DBIP_MONTH      ?= $(shell date -u +%Y-%m)
DBIP_PREV_MONTH ?= $(shell y=$$(date -u +%Y); m=$$(date -u +%m); m=$${m\#0}; if [ $$m -eq 1 ]; then y=$$((y-1)); m=12; else m=$$((m-1)); fi; printf '%d-%02d' $$y $$m)
DBIP_ASN_URL    ?= https://download.db-ip.com/free/dbip-asn-lite-$(DBIP_MONTH).mmdb.gz
DBIP_PREV_URL   ?= https://download.db-ip.com/free/dbip-asn-lite-$(DBIP_PREV_MONTH).mmdb.gz
ASN_NAMES_URL ?= $(GH_PROXY)https://raw.githubusercontent.com/missuo/ASN-China/main/ASN.China.list
IPDATA_BASE   ?= $(GH_PROXY)https://raw.githubusercontent.com/axpwx/IP-Data/master/provider
IPLIST_BASE   ?= https://metowolf.github.io/iplist/data/isp
IPDATA_PROVIDERS := huawei aliyun tencent baidu jdcloud ksyun ucloud aws azure all
IPLIST_PROVIDERS := huawei aliyun tencent baidu ksyun ucloud bytedance
CURL := curl -fL --retry 5 --retry-all-errors

.PHONY: all build test download download-asn-fix download-providers run lookup verify clean

all: build

build:
	go build -o $(BIN) ./cmd/builder

test:
	go test ./...

# Downloads to *.download first so a failed download never clobbers good data.
download:
	@mkdir -p $(DATA_DIR)
	$(CURL) -o $(DATA_DIR)/ipv4_source.txt.download $(XDB_BASE)/ipv4_source.txt
	$(CURL) -o $(DATA_DIR)/ipv6_source.txt.download $(XDB_BASE)/ipv6_source.txt
	$(CURL) -o $(DATA_DIR)/GeoLite2-ASN.mmdb.download $(ASN_URL)
	mv -f $(DATA_DIR)/ipv4_source.txt.download $(DATA_DIR)/ipv4_source.txt
	mv -f $(DATA_DIR)/ipv6_source.txt.download $(DATA_DIR)/ipv6_source.txt
	mv -f $(DATA_DIR)/GeoLite2-ASN.mmdb.download $(DATA_DIR)/GeoLite2-ASN.mmdb
	$(MAKE) download-asn-fix
	$(MAKE) download-providers

# Inputs of config.yaml asn_fix.
download-asn-fix:
	@mkdir -p $(DATA_DIR)
	$(CURL) -o $(DATA_DIR)/delegated-apnic-latest.download $(APNIC_URL)
	$(CURL) -o $(DATA_DIR)/china-operator-ip-china.txt.download $(COIP_BASE)/china.txt
	$(CURL) -o $(DATA_DIR)/china-operator-ip-china6.txt.download $(COIP_BASE)/china6.txt
	$(CURL) -o $(DATA_DIR)/metowolf-china.txt.download https://metowolf.github.io/iplist/data/special/china.txt
	$(CURL) -o $(DATA_DIR)/ip2asn-v4.tsv.gz.download $(IPTOASN_BASE)/ip2asn-v4.tsv.gz
	$(CURL) -o $(DATA_DIR)/ip2asn-v6.tsv.gz.download $(IPTOASN_BASE)/ip2asn-v6.tsv.gz
	$(CURL) -o $(DATA_DIR)/dbip-asn-lite.mmdb.gz.download $(DBIP_ASN_URL) || \
		$(CURL) -o $(DATA_DIR)/dbip-asn-lite.mmdb.gz.download $(DBIP_PREV_URL)
	gunzip -c $(DATA_DIR)/dbip-asn-lite.mmdb.gz.download > $(DATA_DIR)/dbip-asn-lite.mmdb.download
	rm -f $(DATA_DIR)/dbip-asn-lite.mmdb.gz.download
	$(CURL) -o $(DATA_DIR)/ASN.China.list.download $(ASN_NAMES_URL)
	for f in $(DATA_DIR)/*.download; do mv -f "$$f" "$${f%.download}"; done

# Inputs of config.yaml cloud (and optional asn_fix.providers).
download-providers:
	@mkdir -p $(DATA_DIR)/providers
	set -e; for p in $(IPDATA_PROVIDERS); do for f in ipv4 ipv6; do \
		$(CURL) -o $(DATA_DIR)/providers/$$p-cidr-$$f.txt.download $(IPDATA_BASE)/$$p-cidr-$$f.txt; done; done
	set -e; for p in $(IPLIST_PROVIDERS); do \
		$(CURL) -o $(DATA_DIR)/providers/metowolf-$$p.txt.download $(IPLIST_BASE)/$$p.txt; done
	for f in $(DATA_DIR)/providers/*.download; do mv -f "$$f" "$${f%.download}"; done

run: build
	./$(BIN) -config $(CONFIG)
	ls -lh output/China-City-ASN.mmdb

# make lookup IP=114.114.114.114
lookup: build
	./$(BIN) -config $(CONFIG) -lookup $(IP)

# make verify N=200 [MIN=95]; export IPINFO_TOKEN to raise ipinfo.io rate limits.
N   ?= 200
MIN ?= 0
verify: build
	./$(BIN) -config $(CONFIG) -verify $(N) -verify-min-match $(MIN)

clean:
	rm -f $(BIN)
	rm -rf output/*
