# Installing Lolad

Lolad keeps your AI agents running in the background and gives you one window
to talk to them. You do not need to be a developer to install it. It takes
about five minutes.

You need a Mac (macOS 13 or newer). A Windows app is planned but is not released yet.

---

## On a Mac

### 1. Download

Go to the [latest release](https://github.com/CamiloCaceres/lolad/releases/latest)
and download **Lolad.dmg**.

### 2. Drag to Applications

Open the file you downloaded. A small window appears. Drag **Lolad** onto the
**Applications** folder.

### 3. Open it for the first time

Lolad is not sold through the App Store, so your Mac asks you to confirm that
you trust it. You only do this once.

**If your Mac says "Lolad can't be opened" or "Apple could not verify..."**
(this is normal for builds that are not notarized yet):

1. Open the **Applications** folder.
2. **Right-click** (or hold Control and click) **Lolad**, then choose **Open**.
3. A new box appears with an **Open** button. Click it.

If there is no Open button, do this instead:

1. Try to open Lolad once, and close the warning.
2. Open **System Settings**, then **Privacy & Security**.
3. Scroll down. You will see "Lolad was blocked". Click **Open Anyway**.
4. Type your Mac password if asked, then click **Open**.

You will not be asked again.

### 4. Allow it to run in the background

macOS shows a message like "Lolad added items that can run in the
background". That is how Lolad keeps your agents running when the window is
closed. Leave it on. You can change it later in **System Settings, General,
Login Items**.

---

## What happens the first time you open it

Lolad walks you through a short setup:

1. **Start at login.** It asks to start by itself when you sign in to your
   computer, so your agents are always there. You can turn this off later.
2. **Connect an AI tool.** Lolad works with tools such as Claude Code and
   Codex. It shows which ones are already on your computer and helps you
   install or sign in to one. You need at least one.
3. **Pick a folder for your agents.** The suggested folder, **Lolad Agents**
   in your home folder, is a good choice.
4. **Your name.** So agents know who they are talking to.
5. **Your first agent.** Press Start and its first message arrives in your inbox.

You can close the window any time. **Closing the window does not stop your
agents.** They keep working in the background.

---

## Everyday use

- **Open Lolad:** click its icon (the menu bar at the top of the screen), or open it like any other app.
- **Quit the app:** use the icon's menu and choose **Quit Lolad (agents keep
  running)**. The agents do not stop. To stop agents, use the Lolad window.
- **Updates:** Lolad checks for updates by itself and tells you when one is
  ready. You can also choose **Check for updates** from the icon's menu.

---

## If something is stuck

1. Click the Lolad icon, then **View logs**. This opens the folder with the
   latest notes from Lolad. If you ask someone for help, send them the newest
   file there.
2. If the window says Lolad is not running, press **Start**.
3. If that does not work: quit Lolad from the icon's menu and open it again.
4. As a last step, restart your computer. Lolad starts again by itself.

Your agents and messages are stored on your computer and are not lost when you
restart.

---

## Uninstalling

Your agents, mail and settings are **kept** unless you choose otherwise, so you
can install Lolad again later and carry on.

Click the Lolad icon, choose **Uninstall...**, and follow the
questions. It also turns off start at login. (Dragging Lolad to the Trash works
too, but the menu is cleaner.)

---

## Where Lolad keeps your things

| What | Where |
|---|---|
| Settings | `.config/lolad` in your home folder |
| Messages, logs, agent records | `.local/share/lolad` in your home folder |
| Your agents | the folder you picked, usually **Lolad Agents** in your home folder |

Your home folder is the one named after you. Folders whose names start with a
dot are hidden; press Command, Shift, and period in Finder to show them.

---

## For developers

Prefer the terminal? The release page also has the plain `lolad` program for
macOS and Linux, and the project [README](../README.md) shows how to
install from source. How the app is built, signed and updated is in
[desktop.md](desktop.md).
