# **🚀 goSSMS v0.0.12**
Service Broker arrives in Object Explorer, along with a redesigned Connect dialog and smarter IntelliSense.

## 📨 Service Broker
Message types, contracts, queues, services, routes, remote service bindings and broker priorities: listing, details, Properties, scripting and Delete. Queue and Route Properties are editable, and the Queues listing shows message counts.

## 🔌 New Connect dialog
- History pane with your saved connections, most recent first
- Connection Properties and Connection String tabs
- **Reset** and **Delete** buttons
- **Remember Password**, off by default for new connections

## ✨ Also in this release
- IntelliSense completes columns from CTEs, derived tables, sub-SELECTs, `UNION` and `PIVOT`, plus temp tables and table variables; `#` and `@` open the list
- Configurable indent size (Tools > Options) and smart indentation
- Linux desktop launcher and icons, installed by Homebrew and APT
- SQL Server Agent Properties save in one statement
- IntelliSense stays fast in long scripts

## 🔧 Fixes
- Move to Schema now asks for the permission the server actually checks (`CONTROL`)
- Deleting a database-scoped credential or audit specification could fail with "database not found"
- A scripted sequence over an alias type lost the type's schema
- Refreshing a Properties page that was still loading left the old load running

## 📦 Install or upgrade
```
brew install radix29/tap/gossms
```
APT, direct downloads and checksums: <https://github.com/radix29/gossms#installation>

## Links
📋 Release notes — <https://github.com/radix29/gossms/blob/main/RELEASE.md>
⬇️ Downloads — <https://github.com/radix29/gossms/releases/tag/v0.0.12>
