# Cursor through cursor-byok

magpie can manage the model configurations of a separately installed
[cursor-byok](https://github.com/leookun/cursor-byok). The ordinary Cursor
editor sends its requests to cursor-byok, which adapts them to magpie's
gateway. Providers, routing groups, credentials and usage stay in magpie.
This is separate from Cursor CLI, Cursor Private Inference and using a
Cursor subscription as a provider.

## Connect

1. Install and initialize cursor-byok using its own instructions. Keep it
   running. magpie does not install it or configure Cursor, certificates,
   proxies or backend interception.
2. Add your API providers and models in magpie.
3. Open **Agents**, find **Cursor (via cursor-byok)** and connect it. Use
   its model selection to choose which magpie models to offer to Cursor.
4. Restart Cursor, start a new conversation and select a magpie model in
   Cursor's model picker. Keep both magpie and cursor-byok running.

The connection uses cursor-byok's local model management API. It reads the
service port from the existing `~/.cursor-byok-v3/cursor-byok.db` on macOS,
Windows and Linux; it never writes that database directly. This integration
targets the v3 desktop configuration and control API in cursor-byok 1.0.1
([source revision 31e9f5c](https://github.com/leookun/cursor-byok/tree/31e9f5c)).
Older YAML-based releases and separately hosted instances are not detected.

## Model changes and disconnecting

Changes to magpie's selected models are synchronized while cursor-byok is
running. Existing cursor-byok models remain separate. magpie records which
model configurations it created, and disconnecting removes only those
configurations. It does not stop cursor-byok or undo Cursor's setup.

Keep the connection identity of managed models in magpie. Changing a
managed model's endpoint, key, model ID or name in cursor-byok can change
its identity; magpie reports the conflict instead of claiming an unrelated
model. User-created models are never adopted just because their group or
display name says magpie.

If connecting or synchronizing reports an unavailable service, open
cursor-byok and try again. A failed read leaves the ownership record in
place. File previews are unavailable for this API-managed connection.
