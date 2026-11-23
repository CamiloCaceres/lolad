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

Open the **Applications** folder and double-click **Lolad**. If your Mac says
**"Lolad Not Opened"**, **"Apple could not verify..."** or **"Lolad can't be
opened"** (this is normal while Lolad is not notarized by Apple):

1. Click **Done** (or **OK**) to close the warning. Do not move Lolad to the Trash.
2. Open **System Settings**, then **Privacy & Security**.
3. Scroll down to **Security**. You will see "Lolad was blocked". Click
   **Open Anyway**.
4. Type your Mac password (or use Touch ID), then click **Open Anyway** again.

You will not be asked again.

If Lolad says it is running from a temporary place, click **Move to
Applications**. It copies itself there and reopens. It has to live in
Applications to keep your assistants running after a restart.

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

- **Open Lolad:** click its icon (the menu bar at the top of the screen), or open it like any other app.
- **Quit the app:** use the icon's menu and choose **Quit Lolad (agents keep
  running)**. The agents do not stop. To stop agents, use the Lolad window.
- **Updates:** for now, get a new version by downloading the newest
  **Lolad.dmg** from the release page and dragging it to Applications again
  (choose **Replace**). Your assistants, messages and settings are kept.
  Automatic updates will come with the first release signed by Apple.

---

## If something is stuck

1. Click the Lolad icon, then **View logs**. This opens the folder with the
   latest notes from Lolad. If you ask someone for help, send them the newest
   file there.
2. If the window says Lolad is not running, press **Start**.
3. If that does not work: quit Lolad from the icon's menu and open it again.
4. To reconnect or reinstall Claude Code, open **Settings**, then **AI tools**.
5. As a last step, restart your computer. Lolad starts again by itself.

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
