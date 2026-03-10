class Mnemonic < Formula
  desc "Local-first semantic memory system with cognitive agents"
  homepage "https://github.com/CalebisGross/mnemonic"
  license "AGPL-3.0"

  # Updated automatically by release workflow
  # Replace VERSION and SHA256 with actual release values
  on_macos do
    on_arm do
      url "https://github.com/CalebisGross/mnemonic/releases/download/v#{version}/mnemonic_#{version}_darwin_arm64.tar.gz"
      sha256 "PLACEHOLDER"
    end
    on_intel do
      url "https://github.com/CalebisGross/mnemonic/releases/download/v#{version}/mnemonic_#{version}_darwin_amd64.tar.gz"
      sha256 "PLACEHOLDER"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/CalebisGross/mnemonic/releases/download/v#{version}/mnemonic_#{version}_linux_amd64.tar.gz"
      sha256 "PLACEHOLDER"
    end
  end

  def install
    bin.install "mnemonic"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/mnemonic version 2>&1", 2)
  end
end
