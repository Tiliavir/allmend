# 1 Introduction and goals

Allmend (Alemannic: the commons of a village) is the family workbench: an
intranet start page with links to the home network's services, and modules
that are personal, readable by the family, shared or played together.

| Quality goal | Meaning |
| --- | --- |
| No data loss | Every upgrade backs up first, migrates in one transaction, can be rehearsed on a copy. |
| No data leak | Data scope per module enforced in the repositories; LAN only. |
| Lean and fast | Server-rendered HTML, little JS, one static binary, SQLite. |
| Upgradeable | Versioned migrations; a binary refuses a newer database. |
