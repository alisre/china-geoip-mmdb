# china-geoip-mmdb

用 Go 构建 `China-City-ASN.mmdb`：以
[fa1seut0pia/ip2region-xdb](https://github.com/fa1seut0pia/ip2region-xdb)
发布的中国大陆城市数据为基础，按 GeoLite2-ASN 的网段做交集拆分，补充 ASN 编号和组织名称，
输出一份同时支持 IPv4/IPv6 的 MMDB，可直接给 Nginx GeoIP2 模块使用。

> 这是自定义 MMDB，不是官方 GeoIP2 City 数据库。

## 快速开始

```bash
make download   # 下载 ipv4_source.txt / ipv6_source.txt（约 430MB）和 GeoLite2-ASN.mmdb
make run        # 编译并构建 output/China-City-ASN.mmdb
make lookup IP=114.114.114.114
make test
```

或手动：

```bash
go build -o china-geoip-mmdb ./cmd/builder
./china-geoip-mmdb -config config.yaml
./china-geoip-mmdb -config config.yaml -lookup 240e::1
./china-geoip-mmdb -db /path/to/China-City-ASN.mmdb -lookup 1.2.3.4
```

需要 Go 1.24+。

## GitHub Actions 每日构建

[.github/workflows/build.yml](.github/workflows/build.yml) 每天 04:30（北京时间）运行，也可在 Actions 页手动触发：
测试 → 直连下载数据（`GH_PROXY=`）→ 构建并校验 → 发布 Release（tag 为 `YYYYMMDD`，保留最近 30 个）。
配置了仓库 Secret `IPINFO_TOKEN` 时，发布前还会与 ipinfo.io 抽样核对（一致率低于 90% 则不发布）。

```bash
# 始终指向最新一次构建
curl -fLO https://github.com/<owner>/<repo>/releases/latest/download/China-City-ASN.mmdb
curl -fLO https://github.com/<owner>/<repo>/releases/latest/download/China-City-ASN.mmdb.sha256
sha256sum -c China-City-ASN.mmdb.sha256
```

## 数据源

| 文件 | 默认来源 |
|------|----------|
| `data/ipv4_source.txt` | `ip2region-xdb` Release（`XDB_TAG=2026.11.16 make download` 可固定版本） |
| `data/ipv6_source.txt` | 同上 |
| `data/GeoLite2-ASN.mmdb` | `https://raw.githubusercontent.com/Loyalsoldier/geoip/refs/heads/release/GeoLite2-ASN.mmdb`（`ASN_URL=...` 可覆盖） |
| `data/delegated-apnic-latest` | APNIC 分配统计（`CN` 行） |
| `data/china-operator-ip-china{,6}.txt` | [gaoyifan/china-operator-ip](https://github.com/gaoyifan/china-operator-ip) `ip-lists` 分支 |
| `data/metowolf-china.txt` | [metowolf/iplist](https://github.com/metowolf/iplist) `data/special/china.txt` |
| `data/ip2asn-v{4,6}.tsv.gz` | [iptoasn.com](https://iptoasn.com/)（BGP 路由表 origin ASN，每小时更新，PDDL） |
| `data/dbip-asn-lite.mmdb` | DB-IP ASN Lite（`DBIP_MONTH=2026-10` 可指定月份；[lwpk110/free-geoip-databases](https://github.com/lwpk110/free-geoip-databases) 也有镜像） |
| `data/ASN.China.list` | [missuo/ASN-China](https://github.com/missuo/ASN-China) |
| `data/providers/<厂商>-cidr-ipv{4,6}.txt` | [axpwx/IP-Data](https://github.com/axpwx/IP-Data) `provider/`（默认不启用，`make download-providers`） |
| `data/providers/metowolf-<厂商>.txt` | [metowolf/iplist](https://github.com/metowolf/iplist) `data/isp/`（同上） |

`make download-asn-fix` 只下载 ASN 修正相关文件。GitHub 地址默认加 `GH_PROXY=https://gh-proxy.com/` 前缀，
`GH_PROXY= make download` 可直连。

### ASN 修正（`asn_fix`）

GeoLite2-ASN 对国内网段常有错误，例如 `114.116.220.42`（华为云 ECS，实际由电信 IDC AS23724 宣告）被标成
`AS4808 China Unicom Beijing Province Network`。**ASN 以 BGP 宣告方（origin AS）为准**，与 ipinfo.io 一致。
处理思路是先确定完整的中国 IP 集合，再按 BGP 路由表给这些地址生成 origin ASN，GeoLite2 只作兜底：

- `china_ip`：中国 IP 集合 = APNIC `CN` 分配 ∪ china-operator-ip `china`（BGP 路径分类 + 未宣告的 RIR-CN 地址）
  ∪ metowolf `china`。它限定 BGP 层的作用范围，同时与 ip2region 大陆网段交叉核对，日志
  `city source vs china ip set` 输出覆盖率和集合外地址数（集合外的多为上游误标为大陆的地址）。

修正层按以下顺序叠加，后一层优先：

1. `bgp`：iptoasn 的 BGP origin ASN，在中国 IP 集合内覆盖 GeoLite2；未宣告的地址保留 GeoLite2。
2. `fallback_mmdb`：DB-IP ASN Lite，**只填补**仍没有 ASN 的地址，不改已有结果。
3. `providers`（**默认关闭**）：云厂商 CIDR 列表。开启后列表内上游 ASN 不在 `asns` 中的改成 `asns[0]`，
   即把运营商代宣告的云网段改为云厂商自己的 ASN（如 AS23724 → AS55990），与“以宣告方为准”冲突。
4. `overrides`：[overrides/asn_overrides.txt](overrides/asn_overrides.txt) 手工维护的 `CIDR ASN [org]`，无条件覆盖，
   用于修正测试中发现的个别网段。

`org` 留空时，用 `asn_names`（missuo/ASN-China）按 ASN 号补全组织名。日志 `asn layer applied`
会输出每层改动的网段数。

### 与 ipinfo.io 抽样核对

```bash
export IPINFO_TOKEN=xxxx          # 可选；无 token 时限额很低，遇到 429 会提前停止
make verify N=200                 # 均匀抽 200 个带 ASN 的网段，查 ipinfo.io/<ip>/json
make verify N=500 MIN=95          # 一致率低于 95% 时返回非 0
```

stdout 输出不一致的 TSV（`ip our_asn our_org ipinfo_asn ipinfo_org`），stderr 输出一致率。
ipinfo 的 `org` 同样是 BGP origin ASN，默认配置下应基本一致；不一致通常来自中国 IP 集合外、
未宣告（GeoLite2/DB-IP 兜底）、`overrides` 手工修正或两份 BGP 快照的时间差。
这是单独的抽样命令，不在构建流程中联网。

### 上游源文件格式（已按上游 `converter.py` 核对）

```
start_ip|end_ip|洲|国家|省份|城市|区县|ISP|net
```

- 必须正好 9 个字段，否则视为格式错误。
- 中国大陆记录来自 GeoCN，国家列固定写 `中国`；港澳台被上游规范化为 `中国香港`、`中国澳门`、`中国台湾`；
  GeoLite2 中的 CN 网段在上游会被跳过。所以本工具按国家列 **精确等于** `中国` 过滤，
  另外对省份以 `香港`/`澳门`/`台湾` 开头的行做兜底排除（`filter.exclude_provinces`）。
- 中国记录的 `net` 列是 GeoCN 的网络类型（如 `宽带`），**不是 ASN**，不使用；ASN 由 `asn_fix` 各层生成，GeoLite2-ASN 兜底。
- `ipv6_source.txt` 中的 IPv4-mapped（`::ffff:0:0/96`）行会被跳过，IPv4 只取自 `ipv4_source.txt`。
- 上游在 GeoCN 只精确到省级时会把城市置空。对北京、上海、天津、重庆这 4 个直辖市，默认用省份名补齐城市
  （`filter.city_from_province`）；其他省份的城市保持为空。

## 输出结构

```json
{
  "country": { "iso_code": "CN", "names": { "zh-CN": "中国" } },
  "subdivisions": [ { "names": { "zh-CN": "广东省" } } ],
  "city": { "names": { "zh-CN": "深圳市" } },
  "district": "南山区",
  "isp": "中国电信",
  "cloud": "阿里云",
  "autonomous_system_number": 4134,
  "autonomous_system_organization": "CHINANET-BACKBONE"
}
```

空字段不写入；没有 ASN 覆盖的城市网段保留城市信息，不写 ASN 字段。
`district`、`isp`、`cloud` 是自定义字段。`district` 来自上游 GeoCN，约 91% 的大陆 IPv4 网段有区县，
云/IDC 网段通常只到省级。`cloud` 由配置 `cloud` 按 axpwx/IP-Data、metowolf/iplist 的云厂商网段打标
（如 `阿里云`、`华为云`、`IDC`），与 ASN 独立：`114.116.220.42` 为 `asn=23724, cloud=华为云`。元数据 `database_type` 为 `China-City-ASN`。

## 构建流程

1. 检查输入文件存在且非空。
2. 读取 GeoLite2-ASN，IPv4/IPv6 分开，排序并合并相邻同 ASN 网段，再依次叠加 `asn_fix` 修正层。
3. 解析城市源文件：校验字段数、IP、起止顺序、地址族；筛选大陆；排序、去重、检测重叠、合并相邻同数据网段。
4. 双指针做网段交集：城市网段在 ASN 边界处拆分，ASN 前、ASN 之间、城市末尾的空缺保留为无 ASN 段。
5. 写入 IPv6 树（IPv4 自动映射到 `::/96`，并有 `::ffff:0:0/96` 等别名）。
6. 先写到同目录临时文件，再做校验：元数据检查，每个地址族按 `sample_size` 抽样核对网段首尾 IP，
   以及运行 `validate.checks` 中的已知 IP 用例。
7. 全部通过后原子 `rename` 覆盖输出；任何一步失败都不会覆盖已有文件。

日志会输出各阶段统计：输入行数、大陆网段数、港澳台/外国过滤数、ASN 匹配/未匹配网段数、
按网段数和按地址数分别计算的 ASN 覆盖率等。

## 配置

见 [config.yaml](config.yaml)。相对路径以配置文件所在目录为基准。要点：

- `build.skip_invalid_records: false`（默认）：格式错误、重叠网段、保留网段都直接终止构建。
- `build.preserve_city_without_asn: true`：保留无 ASN 的城市网段。
- `validate.min_ipv4_ranges` / `min_ipv6_ranges`：CI 质量门槛，记录数异常下降时终止。
- `validate.checks`：已知 IP 回归用例，`absent: true` 表示该 IP 不应存在（例如境外、港澳台 IP）。

## Nginx

```nginx
geoip2 /etc/nginx/geoIP/China-City-ASN.mmdb {
    $geoip_country   country iso_code;
    $geoip_province  subdivisions 0 names zh-CN;
    $geoip_city      city names zh-CN;
    $geoip_district  district;
    $geoip_isp       isp;
    $geoip_asn       autonomous_system_number;
    $geoip_asn_org   autonomous_system_organization;
}

map "$geoip_city|$geoip_asn" $block_city_asn {
    default 0;
    "深圳市|4134" 1;
}
```

上线前请在实际的 Nginx/模块版本上做查询测试。

## 目录

```
cmd/builder/        入口（构建 + -lookup 查询）及端到端测试
internal/config     配置加载与校验
internal/source     上游城市源文件解析与大陆筛选
internal/asn        GeoLite2-ASN 读取、CIDR/覆盖/ASN 名称列表解析、修正层叠加（Overlay）
internal/merge      网段交集、拆分、空缺处理、覆盖率统计
internal/writer     MMDB 写入（临时文件 + 原子替换）
internal/validate   生成后的查询校验
internal/testutil   测试用合成 ASN MMDB
```

## 许可

GeoLite2（MaxMind）与 GeoCN 的数据许可需分别核对；DB-IP ASN Lite 为 CC BY 4.0，分发时需署名
“IP Geolocation by DB-IP”。公开分发生成的 MMDB 前，请确认各数据源对衍生数据、
再分发和署名的要求。
