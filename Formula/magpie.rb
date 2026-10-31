class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.1725-vb.0.0.8"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.8/magpie-cli-darwin-arm64"
      sha256 "eb84c20ee6b43010e7e42c093f3ddfb0686468698e29bb0079ca39895373d7cd"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.8/magpie-cli-darwin-amd64"
      sha256 "95d5077bee9620ce0ed3b6b8282559bc79f6530d84ccf36ba5665f237b86838c"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.8/magpie-cli-linux-arm64"
      sha256 "5d87d78db43e5025d7578d540a653efaefa52feef27e2ae3bdeeb3e97ccd8dfe"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.1725-vb.0.0.8/magpie-cli-linux-amd64"
      sha256 "e03580ad96592fa99731cca4d3cfb8463d40e05222b4edcc27f3e229f94aca8e"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
