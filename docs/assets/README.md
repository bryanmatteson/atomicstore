# Atomicstore README media

`icon.svg` is an editable vector asset. `demo.gif` records real commands and
output, with no UI or API mocks.

Install Python 3, [VHS](https://github.com/charmbracelet/vhs), and its FFmpeg and
TTY dependencies, plus the project's normal build toolchain. From the root:

```sh
python3 scripts/record-readme-demo.py
```

The recorder builds and runs [`examples/readme/main.go`](../../examples/readme/main.go)
against the actual filesystem backend in a temporary directory. It checks a
stale ETag conflict and reload, refusal of an unsupported atomic batch without
partial writes, lock contention, and a lease handoff with increasing fencing
tokens. The verified output is then recorded through VHS.

The executable, transcript, and working tape are kept in
`~/Documents/Developer/conditional-store/readme`. `README_RECORD_OUTPUT`
overrides that directory. The example removes only the temporary object store
it creates. No S3 account or cloud credentials are used.

The [recording script](../../scripts/record-readme-demo.py) publishes `demo.gif`
only after the example and recording finish successfully.
