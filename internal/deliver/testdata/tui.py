# A raw-mode composer: confirmed submits, staged swallows Enter, failed drops input.
import os, sys, termios, tty
mode = sys.argv[1]
original = termios.tcgetattr(0)
tty.setraw(0)
os.write(1, b'\x1b[?1049h')
text = ''
history = []
submits = 0
def render():
    os.write(1, ('\x1b[2J\x1b[H' + '\r\n'.join(history) + '\r\n' + '─'*210 + '\r\n❯ ' + text + '\r\n' + '─'*210 + '\r\n').encode())
try:
    render()
    while True:
        c = os.read(0, 1)
        if not c: break
        if c in (b'\r', b'\n'):
            submits += 1
            if mode == 'confirmed' or (mode == 'retry' and submits > 1):
                history.append(text)
                text = ''
            if mode == 'failed': text = ''
        elif c in (b'\x7f', b'\x08'):
            text = text[:-1]
        elif mode != 'failed':
            # Decode the full UTF-8 character before repainting.
            if c[0] >= 0xc0:
                n = 2 if c[0] < 0xe0 else 3 if c[0] < 0xf0 else 4
                c += os.read(0, n-1)
            text += c.decode('utf-8', errors='replace')
        render()
finally:
    termios.tcsetattr(0, termios.TCSANOW, original)
