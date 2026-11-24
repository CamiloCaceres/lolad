**Lolad @VERSION@** (beta)

Lolad keeps your AI agents running in the background and gives you one window
to talk to them. Step-by-step install guide:
https://github.com/CamiloCaceres/lolad/blob/main/docs/install.md

## Download

| Your computer | File |
|---|---|
| Mac (macOS 13 or newer, Apple silicon or Intel) | **Lolad.dmg** |
| Windows 11 (64-bit) | **Lolad-Setup.exe** |
| Android phone, to use your Lolad away from your computer ([remote access](https://github.com/CamiloCaceres/lolad/blob/main/docs/remote.md) first) | **Lolad-Android.apk** |
| Terminal only (macOS, Windows, Linux) | the `lolad-<system>-<chip>` files |

## The first time you open it

This beta is not signed by Apple or Microsoft yet. Signing is a paid identity
check. Until Lolad has one, your computer asks you to confirm it once:

- **Mac:** open Lolad from Applications. If it says "Lolad Not Opened", click
  **Done**, then go to **System Settings > Privacy & Security** and click
  **Open Anyway** next to "Lolad was blocked". Confirm with your password or
  Touch ID.
- **Windows:** if a blue "Windows protected your PC" window appears, click
  **More info**, then **Run anyway**. No administrator password is needed.

That message appears for every app that is not signed. It does not mean
anything was found. After this first time, Lolad opens normally, and updates
install without the message.

## Updates

Lolad checks for updates once a day and asks before it restarts. Your agents
keep running during an update@AGENT_RESTART_NOTE@. Every update is checked
against the Lolad project's digital signature before it installs.

## Checking the download (optional)

`checksums.txt` lists the SHA-256 of every file. On a Mac:
`shasum -a 256 Lolad.dmg`. On Windows (PowerShell):
`Get-FileHash Lolad-Setup.exe`.
