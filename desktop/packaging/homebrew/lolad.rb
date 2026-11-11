# Homebrew cask template. The release workflow replaces its placeholders (words between at-signs) and
# attaches the result to the GitHub release as lolad.rb; copy it into the tap
# (e.g. CamiloCaceres/homebrew-lolad, Casks/lolad.rb):
#   brew install --cask camilocaceres/lolad/lolad
cask "lolad" do
  version "@VERSION@"
  sha256 "@DMG_SHA256@"

  url "https://github.com/CamiloCaceres/lolad/releases/download/v#{version}/Lolad-#{version}.dmg"
  name "Lolad"
  desc "Keeps a team of AI agents running, with a window to talk to them"
  homepage "https://github.com/CamiloCaceres/lolad"

  livecheck do
    url :url
    strategy :github_latest
  end

  auto_updates true
  depends_on macos: ">= :ventura"

  app "Lolad.app"

  uninstall launchctl: "dev.caceres.lolad",
            quit:      "dev.caceres.lolad.desktop"

  # `brew uninstall --zap` also deletes lolad's settings and data (never the agents folder).
  zap trash: [
    "~/.config/lolad",
    "~/.local/share/lolad",
  ]
end
