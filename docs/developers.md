# Developers

The project build is straight forward using the `Makefile`; just doing `make` should work. Please ensure you have the following installed prior -

1. GoLang 1.15.x
1. GCC with deps (required for SQLite driver compilation)

That should be it. Tested on _Ubuntu 20.04_ with _GoLang 1.15.4_.

Generate Migration script using command as follows from project root -

```bash
migrate create -ext sql -dir migration/sqls -seq create_sample_table
```
