# **🚀 goSSMS v0.0.13**
Encryption keys arrive in Object Explorer, plus word wrap, a New User dialog and a round of scripting fixes.

## 🔐 Keys and certificates
- Certificates, asymmetric and symmetric keys: listing, details, scripting, Delete, Properties and New
- Certificate backup, Remove Private Key, module signatures
- Database Master Key: Properties, Back Up, Regenerate

## ✨ Also new
- **New User** dialog for every `CREATE USER` form
- Word wrap in the query editor (Alt+Z)
- IntelliSense matches anywhere in a name
- Unsaved queries are saved if gossms crashes or the terminal closes
- `GO n` repeats a batch

## 🔧 Fixes
- `GO -- step 2` ran the batch twice
- Script Table as CREATE dropped scales, CHECK constraints and index options
- Included Columns rebuilt the index with different options
- Detach, rename and Restore raced for single-user access
- Non-ASCII file paths were mangled

## ⚡ Changes
- Large Results to Text sets format in the background
- Two running instances no longer overwrite each other's connections

## 📦 Install or upgrade
```
brew install radix29/tap/gossms
```
APT, direct downloads and checksums: <https://github.com/radix29/gossms#installation>

## Links
📋 Release notes — <https://github.com/radix29/gossms/blob/main/RELEASE.md>
⬇️ Downloads — <https://github.com/radix29/gossms/releases/tag/v0.0.13>
