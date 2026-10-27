Release builds place the target's `magpie-cursor-backend` executable here
(`magpie-cursor-backend.exe` on Windows) before compiling Go. The executable
is built from the pinned sources in `native/cursor-backend` and embedded in
Magpie; generated binaries must not be committed.

A source build without this payload still compiles, but the managed Cursor
connection reports that the backend is unavailable. It never downloads or
executes a separately installed copy.
