class Magpie < Formula
  desc "Pick the model each AI coding agent on your machine uses"
  homepage "https://github.com/zwenooo/magpie"
  version "0.1.2909-vb.0.0.5"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2909-vb.0.0.5/magpie-cli-darwin-arm64"
      sha256 "e32967d14725f5e985003670abeb3f1f01ad4fdcc28fab4e52635fde266c1c68"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2909-vb.0.0.5/magpie-cli-darwin-amd64"
      sha256 "2ab8af8b57b244df253902a0a8e604aa04b1a6bf70c2208e9e7294aae955af8e"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2909-vb.0.0.5/magpie-cli-linux-arm64"
      sha256 "3e7eff0832f82e67b9c88f5192f7b018200e2b159da1bb1aaff8c8da2b51c011"
    end
    on_intel do
      url "https://github.com/zwenooo/magpie/releases/download/va.0.1.2909-vb.0.0.5/magpie-cli-linux-amd64"
      sha256 "8138c2be8857323d442d0b9a4f2f96b2baae95c4fddc9c37ab78a7233f25a734"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
