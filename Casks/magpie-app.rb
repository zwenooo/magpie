cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "0.1.1725-vb.0.0.6"
  sha256 arm:   "9266a5f0c1565df74ab7b3b0ad4917c93f63a21345d2d9d829bbfc03d0898c66",
         intel: "df6785e178794151638a015414617668929b4f3e036de12ea961cf9c05f26385"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.6/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on :macos

  app "magpie.app"
end
