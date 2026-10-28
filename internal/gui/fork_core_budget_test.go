package gui

// The fork's existing Agents page adds local Cursor setup consent, its model
// picker before connection, connection guidance, and the CodeZ PNG registry.
// Measured against official v0.1.1725: 1,311,560 - 1,310,129 = 1,431 bytes.
// Keep this explicit allowance separate from upstream's page budgets; it adds
// no spare room and does not exempt unrelated core script growth.
const forkCoreScriptBytes = 1_431
