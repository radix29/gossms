There aren't Unicode characters that exactly match the colorful icons in SQL Server Management Studio (SSMS), but you can get surprisingly close using Unicode symbols and emoji. Here are some good approximations for an Object Explorer-style tree.

| SSMS Object      | Unicode | Character |
| ---------------- | ------- | --------- |
| Server           | 🖥      | 🖥        |
| Database         | 🛢      | 🛢        |
| Folder           | 📁      | 📁        |
| Tables           | ▦       | ▦         |
| Table            | ▤       | ▤         |
| View             | 👁      | 👁        |
| Stored Procedure | ⚙       | ⚙         |
| Function         | ƒ       | ƒ         |
| Trigger          | ⚡       | ⚡         |
| Key              | 🔑      | 🔑        |
| Index            | 📇      | 📇        |
| User             | 👤      | 👤        |
| Security         | 🔒      | 🔒        |
| Login            | 🔐      | 🔐        |
| Schema           | 🧩      | 🧩        |
| Column           | │       | │         |
| Primary Key      | 🗝      | 🗝        |
| Foreign Key      | 🔗      | 🔗        |
| Constraint       | ⛓       | ⛓         |
| Filegroup        | 💾      | 💾        |
| Job              | ⏱       | ⏱         |
| Agent            | 🤖      | 🤖        |
| Backup           | 💿      | 💿        |
| Restore          | ♻       | ♻         |

### Example tree

```text
🖥 SQLSERVER01
├── 🛢 AdventureWorks
│   ├── 📁 Tables
│   │   ├── ▤ Person
│   │   │   ├── 🔑 PK_Person
│   │   │   ├── 🔗 FK_Address
│   │   │   └── 📇 IX_LastName
│   │   └── ▤ Sales
│   ├── 👁 Views
│   ├── ⚙ Stored Procedures
│   ├── ƒ Functions
│   └── 🔒 Security
└── ⏱ SQL Server Agent
```

## If you want a more "technical" look

Instead of emoji, use geometric Unicode symbols. They blend much better in monospace fonts and look more like SSMS:

| Meaning    | Suggested Symbol |
| ---------- | ---------------- |
| Server     | ◉                |
| Database   | ⬢                |
| Folder     | ▸ / ▾            |
| Table      | ▦                |
| View       | ◫                |
| Procedure  | ⚙                |
| Function   | λ                |
| Key        | 🔑 or ⚿          |
| Index      | ⌗                |
| Column     | │                |
| Constraint | ⛓                |
| Trigger    | ⚡                |

Example:

```text
◉ SQLSERVER01
├─ ⬢ AdventureWorks
│  ├─ ▦ Tables
│  │  ├─ ▦ Person
│  │  │  ├─ ⚿ PK_Person
│  │  │  ├─ ⌗ IX_LastName
│  │  │  └─ ⛓ FK_Address
│  ├─ ◫ Views
│  ├─ ⚙ Procedures
│  └─ λ Functions
```

## If your output is intended for terminals

I'd recommend avoiding emoji because they:

* can be double-width in some terminals,
* may not align in monospace layouts,
* vary significantly between operating systems.

A more portable set is:

```text
◉  Server
⬢  Database
▸  Folder (closed)
▾  Folder (open)
▦  Table
◫  View
⚙  Procedure
λ  Function
⌗  Index
⚿  Key
⛓  Constraint
⚡  Trigger
•  Column
```

This combination renders consistently in modern UTF-8 terminals on Windows Terminal, Linux terminal emulators, and macOS Terminal while maintaining a clean, developer-oriented appearance.
