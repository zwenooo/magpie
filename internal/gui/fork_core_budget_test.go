package gui

// The fork's existing Agents page adds local Cursor setup consent, its model
// picker before connection, connection guidance, and the CodeZ PNG registry.
// Measured with codeBytes against upstream b332ef7ef: 1,089,932 - 1,088,611.
// +101 for the Agents line of an agent not connected whose config still
// sends to magpie (connectinfo.go's "magpie" source), one line in
// connectLine, which every Agents row draws.
const forkCoreScriptBytes = 1_422
