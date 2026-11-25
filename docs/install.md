# Installing Lolad

Lolad keeps your AI agents running in the background and gives you one window
to talk to them. You do not need to be a developer to install it. It takes
about five minutes.

You need a Mac (macOS 13 or newer) or a Windows 11 PC.

> **This is a beta.** Lolad is not yet signed by Apple or Microsoft. Signing is
> a paid identity check that tells your computer who made an app. Without it,
> your Mac or PC shows one extra warning the first time you open Lolad. The
> steps below walk you through it. It takes a minute, and you only do it once.
> Updates after that install without the warning.

---

## Mac

### 1. Download

Go to the [latest release](https://github.com/CamiloCaceres/lolad/releases/latest)
and download **Lolad.dmg**.

### 2. Drag to Applications

Open the file you downloaded. A small window appears. Drag **Lolad** onto the
**Applications** folder.

### 3. Open it for the first time

Open the **Applications** folder and double-click **Lolad**. Because this beta
is not signed by Apple yet, your Mac asks you to confirm it the first time. You
may see **"Lolad Not Opened"** or **"Apple could not verify 'Lolad' is free of
malware"**. That message appears for every app that is not signed by Apple. It
does not mean anything was found.

1. Click **Done** to close the message. (Do not choose Move to Trash.)
2. Open **System Settings**, then **Privacy & Security**.
3. Scroll down to the **Security** section. Next to "Lolad was blocked to
   protect your Mac", click **Open Anyway**.
4. Confirm with your Mac password or Touch ID, then click **Open Anyway**
   once more.

Lolad opens. Your Mac remembers the choice, so you will not see this again,
including after updates.

If Lolad says it is running from a temporary place, click **Move to
Applications**. It copies itself there and reopens. It has to live in
Applications to keep your assistants running after a restart.

### 4. Allow it to run in the background

macOS shows a message like "Lolad added items that can run in the
background". That is how Lolad keeps your agents running when the window is
closed. Leave it on. You can change it later in **System Settings, General,
Login Items**.

---

## Windows

You need Windows 11 on a 64-bit Intel or AMD PC, and about 200 MB of free
space. No administrator account is needed.

### 1. Download

Go to the [latest release](https://github.com/CamiloCaceres/lolad/releases/latest)
and download **Lolad-Setup.exe**.

If your browser says the file "isn't commonly downloaded", choose **Keep**
(in Edge: the **...** menu next to the download, then **Keep**, then
**Keep anyway**). This appears for new programs that few people have
downloaded yet.

### 2. Run the installer

Double-click **Lolad-Setup.exe**. Because this beta is not signed yet, Windows
may show a blue **"Windows protected your PC"** window first:

1. Click **More info**.
2. Check that the app name is **Lolad-Setup.exe**, then click **Run anyway**.

The installer does not ask for an administrator password. Lolad installs only
for your Windows account, in your own folder. Click **Install**. At the end,
leave **Open Lolad now** ticked and click **Finish**.

### 3. Lolad opens

Lolad starts and shows its setup. Its icon lives in the notification area at
the bottom right of the screen. Click the **^** arrow if you do not see it.

---

## What happens the first time you open it

Lolad walks you through a short setup:

1. **Start at login.** It asks to start by itself when you sign in to your
   computer, so your agents are always there. You can turn this off later.
2. **Connect an AI tool.** Lolad's assistants think with **Claude Code**, so
   you need a Claude account (a paid Claude plan, or an Anthropic API key).
   Lolad checks whether Claude Code is already on your computer, and if not,
   the **Install** button gets it for you. Then press **Sign in**. A browser
   window opens to log in to your Claude account.
3. **Pick a folder for your agents.** The suggested folder, **Lolad Agents**
   in your home folder, is a good choice.
4. **Your name.** So agents know who they are talking to.
5. **Your first agent.** Press Start and its first message arrives in your inbox.

You can close the window any time. **Closing the window does not stop your
agents.** They keep working in the background.

---

## Everyday use

- **Open Lolad:** click its icon (in the menu bar at the top of the screen on a
  Mac, or the notification area at the bottom right on Windows), or open it
  like any other app.
- **Quit the app:** use the icon's menu and choose **Quit Lolad (agents keep
  running)**. The agents do not stop. To stop agents, use the Lolad window.
- **Updates:** Lolad checks once a day. When a new version is ready, the
  icon's menu shows **Restart to update**. Choose it when it suits you; your
  agents keep running. You can also choose **Check for updates...** at any
  time. Every update is checked against a digital signature from the Lolad
  project before it installs, so only real Lolad releases are accepted. If an
  update ever cannot install by itself, download the newest file from the
  release page and install it over the old one. Your agents, messages and
  settings are kept.

---

## If something is stuck

1. Click the Lolad icon, then **View logs**. This opens the folder with the
   latest notes from Lolad. If you ask someone for help, send them the newest
   file there.
2. If the window says Lolad is not running, press **Start**.
3. If that does not work: quit Lolad from the icon's menu and open it again.
4. To reconnect or reinstall Claude Code, open **Settings**, then **Claude**.
   If an agent says **"Claude Code isn't installed"**, install it there (on
   Windows you can also run `irm https://claude.ai/install.ps1 | iex` in
   PowerShell). The agent starts by itself within half a minute of Claude Code
   being found. You do not need to restart Lolad or sign out of Windows.
5. As a last step, restart your computer. Lolad starts again by itself.

Your agents and messages are stored on your computer and are not lost when you
restart.

---

## Uninstalling

Your agents, mail and settings are **kept** unless you choose otherwise, so you
can install Lolad again later and carry on.

- **Mac:** click the Lolad icon, choose **Uninstall...**, and follow the
  questions. It also turns off start at login. (Dragging Lolad to the Trash
  works too, but the menu is cleaner.)
- **Windows:** open **Settings**, **Apps**, **Installed apps**, find **Lolad**,
  and choose **Uninstall**. It asks whether to stop your agents, and only
  deletes your data if you tick that box and confirm.

---

## Where Lolad keeps your things

| What | Where |
|---|---|
| Settings | `.config/lolad` in your home folder |
| Messages, logs, agent records | `.local/share/lolad` in your home folder |
| The program (Windows) | `AppData\Local\Programs\Lolad` in your user folder |
| Your agents | the folder you picked, usually **Lolad Agents** in your home folder |

Your home folder is the one named after you. Folders whose names start with a
dot are hidden; press Command, Shift, and period in Finder to show them.

---

## For developers

Prefer the terminal? The release page also has the plain `lolad` program for
macOS, Windows and Linux, and the project [README](../README.md) shows how to
install from source. How the app is built, signed and updated is in
[desktop.md](desktop.md).
