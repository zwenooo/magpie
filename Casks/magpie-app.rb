cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "0.1.1725-vb.0.0.8"
  sha256 arm:   "0695001b882264f94b30c8e94cf8fce7d9ca39a00302b4e3d86bce9cc8f3f38d",
         intel: "2bebaa30a8c856074f29b9e0623412501a49d7bf6db55c05ab144ec119383a96"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.8/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on :macos

  app "magpie.app"
end
