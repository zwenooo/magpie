cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "0.1.2549-vb.0.0.2"
  sha256 arm:   "3103527b32e7e76401e656a80928c503394e4d73dac8c1a527208fdb8c88c83d",
         intel: "da0481e62d6c1f062b532692d5c621be23bce3c53b4c44858897e25a32a74052"

  url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2549-vb.0.0.2/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"

  auto_updates true
  depends_on :macos

  app "magpie.app"
end
