# Compression

Closed days are compressed to verified zstd archives by a background sweep.

Governed configuration: `ARCHIVE_DIR`, `ZSTD_LEVEL`.

## Sweep

A single background worker runs the sweep, triggered at startup and after each
rotation. For every plain `.ndjson` whose date is older than today it:

1. writes `<name>.ndjson.zst` (overwriting any leftover),
2. fsyncs the `.zst`,
3. decodes the `.zst` and byte-compares it against the plain file,
4. fsyncs the directory,
5. deletes the plain file.

The plain file is deleted only after step 3 confirms a byte-identical match. On a
mismatch or any error the plain file is kept, the failure is logged, and the
sweep continues with the next file. A plain file sitting next to a `.zst` marks
that `.zst` as untrusted, so it is redone on the next sweep. The sweep is
idempotent — safe to interrupt and re-run at any point — and operates only on
days older than today, so it stays clear of the active writer.

## Closed archive format

`<ARCHIVE_DIR>/YYYY-MM-DD.ndjson.zst` — a single zstd frame with a content
checksum, decoding byte-identical to the plain file it replaced. Readable with
stock `zstdcat` and checkable with `zstd -t`.

## Directory states

| State | Meaning |
|---|---|
| `.ndjson` only | pending compression |
| `.ndjson` + `.ndjson.zst` | `.zst` untrusted — will be redone |
| `.ndjson.zst` only | final, verified |
