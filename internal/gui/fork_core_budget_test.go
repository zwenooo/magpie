package gui

// The fork's existing Agents page adds local Cursor setup consent, its model
// picker before connection, connection guidance, and the CodeZ PNG registry.
// Update mirror wording and deferred header fitting add 251 bytes (net), so
// WebKit can resize the update pill without a ResizeObserver delivery loop.
// Measured against official v0.1.1725: 1,311,811 - 1,310,129 = 1,682 bytes.
// Keep this explicit allowance separate from upstream's page budgets; it adds
// no spare room and does not exempt unrelated core script growth.
const forkCoreScriptBytes = 1_682
