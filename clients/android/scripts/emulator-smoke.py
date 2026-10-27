#!/usr/bin/env python3
"""Exercise only the isolated API/emulator. Never prints pairing codes or tokens."""
import json, os, pathlib, re, subprocess, sys, time, urllib.parse, urllib.request, xml.etree.ElementTree as ET
ADB=os.path.expanduser('~/android/sdk/platform-tools/adb')
ROOT=pathlib.Path(__file__).resolve().parents[1]
SHOTS=ROOT/'docs/screenshots'
def adb(*args):
    return subprocess.check_output([ADB,*args],stderr=subprocess.DEVNULL)
def nodes():
    adb('shell','uiautomator','dump','/sdcard/lolad-test-window.xml')
    raw=adb('shell','cat','/sdcard/lolad-test-window.xml').decode()
    start=raw.find('<?xml')
    return ET.fromstring(raw[start:raw.rfind('</hierarchy>')+12]).iter('node')
def tap(label, exact=True):
    for n in nodes():
        text=n.get('text','')
        if (text==label if exact else label in text):
            x1,y1,x2,y2=map(int,re.findall(r'\d+',n.get('bounds')))
            adb('shell','input','tap',str((x1+x2)//2),str((y1+y2)//2));time.sleep(.6);return
    raise RuntimeError('Missing control: '+label)
def exists(label):
    return any(label in n.get('text','') for n in nodes())
def wait(label):
    for _ in range(5):
        if exists(label): return
        time.sleep(1)
    raise RuntimeError('Timed out: '+label)
def shot(name):
    SHOTS.mkdir(parents=True,exist_ok=True)
    (SHOTS/name).write_bytes(adb('exec-out','screencap','-p'))
def pair(port):
    req=urllib.request.Request('http://127.0.0.1:4193/api/devices/pair',data=b'{"name":"Android emulator"}',headers={'Content-Type':'application/json'})
    code=json.load(urllib.request.urlopen(req))['code']
    link='lolad://pair?u='+urllib.parse.quote('http://10.0.2.2:'+str(port),safe='')+'&c='+code
    adb('shell','am','start','-a','android.intent.action.VIEW','-d',"'"+link+"'",'dev.caceres.lolad')
    time.sleep(.5);tap('Pair device');wait('Conversations')

def main():
    if "--resume-revoked" in sys.argv:
        wait("revoked");shot("revoked-light.png");dark_checks();return
    if "--resume-terminal" in sys.argv:
        wait("Ready for your message");shot("terminal-light.png");remaining();return
    # Trusted 4193 proves the assigned emulator route; 4183 proves Bearer authentication.
    adb('shell','pm','clear','dev.caceres.lolad')
    adb('shell','am','start','-n','dev.caceres.lolad/.MainActivity')
    time.sleep(1)
    pair(4193)
    wait('Waiting on you')
    shot('inbox-light.png')
    tap('Open conversation');assert exists('Waiting on you')
    shot('chat-light.png')
    tap('Proceed');time.sleep(2);assert exists('You chose Proceed')
    for n in nodes():
        if n.get('class')=='android.widget.EditText':
            x1,y1,x2,y2=map(int,re.findall(r'\d+',n.get('bounds')));adb('shell','input','tap',str((x1+x2)//2),str((y1+y2)//2));break
    adb('shell','input','text','Hello%sthe%sisolated%sAndroid%stest')
    time.sleep(1);tap('Send');time.sleep(2)
    adb('shell','input','keyevent','111');time.sleep(.5)
    sent=json.load(urllib.request.urlopen('http://127.0.0.1:4193/api/mail/conversation/iris'))
    assert any(m.get('from_name')=='milo' and m.get('body')=='Hello the isolated Android test' for m in sent)
    shot('chat-reply-light.png')
    tap('Agent details');assert exists('Harness');shot('agent-light.png')
    tap('View terminal snapshot');wait('Ready for your message');shot('terminal-light.png')
    remaining()
def remaining():
    tap('Messages')
    for _ in range(4):
        if exists('Conversations'): break
        tap('Back')
    tap('Agents',False);assert exists('Your agents');shot('agents-light.png')
    tap('Disconnect');tap('Disconnect');time.sleep(.5)
    pair(4183);assert exists('Conversations')
    # Revoke just the most recently paired emulator device via trusted throwaway API.
    devices=json.load(urllib.request.urlopen('http://127.0.0.1:4193/api/devices'))
    # auth store lists newest first; inspect only public IDs/name and never credentials.
    candidates=[d for d in devices if d.get('kind')=='android']
    current=max(candidates,key=lambda d:d.get('created',''))
    req=urllib.request.Request('http://127.0.0.1:4193/api/devices/'+current['id'],method='DELETE')
    urllib.request.urlopen(req).close()
    if exists('Refresh'): tap('Refresh')
    wait('revoked');shot('revoked-light.png')
    dark_checks()
def dark_checks():
    pair(4183);tap('Open conversation')
    adb('shell','cmd','uimode','night','yes');time.sleep(2);shot('chat-dark.png')
    tap('Back');shot('inbox-dark.png');tap('Agents',False);shot('agents-dark.png')
    tap('View agent');shot('agent-dark.png');tap('View terminal snapshot');time.sleep(2);shot('terminal-dark.png')
    adb('shell','settings','put','system','font_scale','1.3');time.sleep(2);shot('terminal-dark-large-text.png')
    adb('shell','settings','put','system','font_scale','1.0')
    adb('shell','cmd','uimode','night','no')
    print('PASS: paired trusted and authenticated listeners; list, ask decision, reply, details, terminal, revocation and light/dark captures.')
if __name__=='__main__':main()
