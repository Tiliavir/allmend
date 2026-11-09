# ADR-20261109: Ground-up rewrite in the same repo

Status: Accepted

## Decision
Allmend is rebuilt from scratch on branch `allmend` in the existing repo and
merged as one major release (2.0.0). Domain logic worth keeping (money,
commute calculation, finance calculations, bike aggregates) is carried over
deliberately, file by file, not copied wholesale.

## Why
Accounts, per-module access and data ownership touch every layer; patching the
generic connector model is more work than rebuilding. Same repo keeps history,
issues and CI.
