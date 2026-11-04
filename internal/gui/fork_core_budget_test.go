package gui

// The fork's existing Agents page adds local Cursor setup consent, its model
// picker before connection, connection guidance, and the CodeZ PNG registry.
// Update mirror wording and deferred header fitting add 251 bytes (net), so
// WebKit can resize the update pill without a ResizeObserver delivery loop.
// Measured against upstream 0ee424e3e: 1,313,710 - 1,312,028 = 1,682 bytes.
// Keep this explicit allowance separate from upstream's page budgets; it adds
// no spare room and does not exempt unrelated core script growth.
const forkCoreScriptBytes = 1_682
