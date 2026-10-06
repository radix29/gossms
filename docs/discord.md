# **🚀 goSSMS v0.0.14**
Resource Governor and Database Mail testing arrive, plus smarter IntelliSense across databases.

## ✨ New
- **Resource Governor**: pools, workload groups, live counters, Properties
- Database Mail **Send Test E-Mail**
- IntelliSense across databases and linked servers
- Extended Events grouping and aggregation
- `PIVOT`, `OPENJSON`, `OPENROWSET`, `OPENXML` parsing

## 🔧 Fixes
- Agent schedules sharing a name hit the wrong one
- Name checks now follow the database collation
- Failed Database Mail Apply left settings changed
- Instances on one host shared credentials
- Identifiers with `$`, `#`, `@`; tabs and line breaks in grids
- Cancelled tasks were reported as errors

## ⚡ Changes
- `gosmo` v0.0.16
- Faster typing in large scripts

## 📦 Install or upgrade
```
brew install radix29/tap/gossms
```
APT, downloads, checksums: <https://github.com/radix29/gossms#installation>

📋 Notes — <https://github.com/radix29/gossms/blob/main/RELEASE.md>
⬇️ Downloads — <https://github.com/radix29/gossms/releases/tag/v0.0.14>
