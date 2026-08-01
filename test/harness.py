#!/usr/bin/env python3
"""Isolated-server integration harness for herdr-smartnav.

Boots a throwaway herdr server with state fully redirected (XDG_CONFIG_HOME),
builds the [A | [B, C]] layout, runs the smartnav daemon, drives spends via the
socket, invokes the action binary, and asserts the focused pane matches the
expected tmux-style target. No live ~/.config/herdr is touched.

Usage: python3 test/harness.py [--keep]
"""
import json, os, signal, socket, subprocess, sys, time, shutil, tempfile, pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
BIN = ROOT / "herdr-smartnav"

class Herdr:
    def __init__(self, root):
        self.root = pathlib.Path(root)
        (self.root/"xdg/herdr").mkdir(parents=True, exist_ok=True)
        (self.root/"run").mkdir(parents=True, exist_ok=True)
        (self.root/"state").mkdir(parents=True, exist_ok=True)
        (self.root/"xdg/herdr/config.toml").write_text('[keys]\nprefix = "ctrl+s"\n')
        self.sock = str(self.root/"run/sock")
        self.csock = str(self.root/"run/client.sock")
        self.env = {
            "XDG_CONFIG_HOME": str(self.root/"xdg"),
            "HERDR_SOCKET_PATH": self.sock,
            "HERDR_CLIENT_SOCKET_PATH": self.csock,
            "PATH": os.environ["PATH"],
            "HOME": os.environ["HOME"],
        }
        self._n = 0
        self.proc = None

    def _call(self, method, params=None, timeout=6):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.connect(self.sock)
        self._n += 1
        req = {"jsonrpc":"2.0","id":str(self._n),"method":method}
        req["params"] = params if params is not None else {}
        s.sendall((json.dumps(req)+"\n").encode()); s.settimeout(timeout)
        buf=b""
        while b"\n" not in buf:
            d=s.recv(65536)
            if not d: break
            buf+=d
        s.close()
        resp=json.loads(buf.decode())
        if "error" in resp: raise RuntimeError(f"{method}: {resp['error']}")
        return resp.get("result")

    def start(self):
        for f in (self.sock, self.csock):
            try: os.remove(f)
            except FileNotFoundError: pass
        log=open(self.root/"server.log","wb")
        self.proc=subprocess.Popen(["herdr","server"],env=self.env,stdout=log,stderr=subprocess.STDOUT)
        log.close()
        for _ in range(50):
            if os.path.exists(self.sock): break
            time.sleep(0.1)
        else: raise RuntimeError("server didn't start")

    def stop(self):
        try: self._call("server.stop",{})
        except Exception: pass
        try: self.proc.wait(timeout=5)
        except Exception:
            if self.proc.poll() is None: self.proc.kill()

    def workspace_create(self):
        r=self._call("workspace.create",{"focus":True,"cwd":str(self.root)})
        return r["workspace"]["workspace_id"], r["tab"]["tab_id"], r["root_pane"]["pane_id"]

    def layout_apply_abc(self):
        r=self._call("layout.apply",{"root":{
            "type":"split","direction":"right","ratio":0.5,
            "first":{"type":"pane"},
            "second":{"type":"split","direction":"down","ratio":0.5,
                      "first":{"type":"pane"},"second":{"type":"pane"}}}})
        t=r["layout"]["tab_id"]; root=r["layout"]["root"]
        A=root["first"]["pane_id"]; B=root["second"]["first"]["pane_id"]; C=root["second"]["second"]["pane_id"]
        return t,A,B,C

    def layout_apply_nested_hh(self):
        # [[A,B],[C,D]] — nested HORIZONTAL splits. From A, right must go to B
        # (innermost split), not C — tests innermost-ancestor resolution.
        r=self._call("layout.apply",{"root":{
            "type":"split","direction":"right","ratio":0.5,
            "first":{"type":"split","direction":"right","ratio":0.5,
                     "first":{"type":"pane"},"second":{"type":"pane"}},
            "second":{"type":"split","direction":"right","ratio":0.5,
                      "first":{"type":"pane"},"second":{"type":"pane"}}}})
        t=r["layout"]["tab_id"]; root=r["layout"]["root"]
        A=root["first"]["first"]["pane_id"]; B=root["first"]["second"]["pane_id"]
        C=root["second"]["first"]["pane_id"]; D=root["second"]["second"]["pane_id"]
        return t,A,B,C,D

    def close(self, pane_id):
        self._call("pane.close",{"pane_id":pane_id})

    def focus(self, pane_id):
        self._call("pane.focus",{"pane_id":pane_id})

    def toggle_zoom(self):
        self._call("pane.zoom",{})

    def focused_pane(self):
        r=self._call("pane.layout",{})
        return r["layout"]["focused_pane_id"]

    def layout_for(self, pane_id):
        return self._call("pane.layout",{"pane_id":pane_id})["layout"]

def wait_state(state_dir, ws, tab, predicate, timeout=5):
    f=state_dir/f"smart.{ws}.{tab}.json"
    end=time.time()+timeout
    while time.time()<end:
        if f.exists():
            try:
                st=json.loads(f.read_text())
                if predicate(st): return st
            except Exception: pass
        time.sleep(0.05)
    return None

def run_action(env, direction):
    action_env=dict(env)
    a=subprocess.run([str(BIN),direction],env=action_env,capture_output=True,text=True,timeout=8)
    return a

def main():
    keep="--keep" in sys.argv
    tmp=pathlib.Path(tempfile.mkdtemp(prefix="smartnav-test-"))
    try:
        h=Herdr(tmp); h.start()
        ws,tab0,p0=h.workspace_create()
        # build [A | [B, C]] in a new tab, focus moves to A
        tab,A,B,C=h.layout_apply_abc()
        print(f"layout: tab={tab} A={A} B={B} C={C}")
        # start daemon pointing at isolated socket + state dir
        state_dir=tmp/"state"
        daemon_env=dict(h.env)
        daemon_env["HERDR_PLUGIN_STATE_DIR"]=str(state_dir)
        daemon_env["HERDR_SOCKET_PATH"] = str(tmp/"run/sock")
        daemon_env["HERDR_CLIENT_SOCKET_PATH"]=str(tmp/"run/client.sock")
        # daemon reads HERDR_SOCKET_PATH for client+subscribe (already set)
        if not BIN.exists():
            print("!! binary missing — run: go build -o herdr-smartnav ."); return 1
        dproc=subprocess.Popen([str(BIN),"daemon"],env=daemon_env,stdout=open(tmp/"daemon.out","wb"),stderr=subprocess.STDOUT)
        try:
            # give daemon time to bootstrap
            time.sleep(0.8)

            action_env_base=dict(h.env)
            action_env_base["HERDR_PLUGIN_STATE_DIR"]=str(state_dir)
            action_env_base["HERDR_PANE_ID"]=A
            action_env_base["HERDR_TAB_ID"]=tab
            action_env_base["HERDR_WORKSPACE_ID"]=ws

            failures=0
            def check(name, cond, extra=""):
                nonlocal failures
                ok = bool(cond)
                print(f"  [{'PASS' if ok else 'FAIL'}] {name} {extra}")
                if not ok: failures+=1

            # --- Scenario 1: focus C, then A, then right -> expect C
            h.focus(C)
            if not wait_state(state_dir,ws,tab,lambda st: C in st.get("last_active_per_split",{}).values()):
                print("  [WARN] daemon didn't record C (event race?)")
            h.focus(A)
            wait_state(state_dir,ws,tab,lambda st: st.get("last_focused_pane")==A)
            run_action(action_env_base,"right")
            f=h.focused_pane(); check("right from A (C last) -> C", f==C, f"(got {f}, want {C})")

            # --- Scenario 2: focus B, then A, then right -> expect B
            h.focus(B)
            wait_state(state_dir,ws,tab,lambda st: B in st.get("last_active_per_split",{}).values())
            h.focus(A)
            run_action(action_env_base,"right")
            f=h.focused_pane(); check("right from A (B last) -> B", f==B, f"(got {f}, want {B})")

            # --- Scenario 3: from C, up -> B (single-pane sibling)
            h.focus(C)
            action_env_up=dict(action_env_base); action_env_up["HERDR_PANE_ID"]=C
            run_action(action_env_up,"up")
            f=h.focused_pane(); check("up from C -> B", f==B, f"(got {f}, want {B})")

            # --- Scenario 4: left from B -> A (single-pane sibling on horizontal axis)
            h.focus(B)
            action_env_b=dict(action_env_base); action_env_b["HERDR_PANE_ID"]=B
            run_action(action_env_b,"left")
            f=h.focused_pane(); check("left from B -> A", f==A, f"(got {f}, want {A})")

            # --- Scenario Z: zoomed tab -> action delegates to focus_direction
            h.focus(A)
            h.toggle_zoom()
            lz = h.layout_for(A)
            check("zoomed reports true", lz.get("zoomed") is True, f"(got zoomed={lz.get('zoomed')})")
            action_env_za = dict(action_env_base); action_env_za["HERDR_PANE_ID"] = A
            rza = run_action(action_env_za, "right")
            fza = h.focused_pane()
            check("zoomed right from A -> navigates (not stuck)", fza != A and rza.returncode == 0, f"(got {fza}, rc={rza.returncode})")
            h.toggle_zoom()  # unzoom

            # --- Scenario 5: close C -> right from A -> B (split_1_1 collapses)
            h.focus(C); time.sleep(0.2)
            h.close(C)
            new_focus = h.focused_pane()
            wait_state(state_dir,ws,tab,lambda st: st.get("last_focused_pane")==new_focus,timeout=3)
            action_env_a=dict(action_env_base); action_env_a["HERDR_PANE_ID"]=A
            run_action(action_env_a,"right")
            f=h.focused_pane(); check("right from A after close C -> B", f==B, f"(got {f}, want {B})")

            # --- Scenario 6: nested horizontal [[A,B],[C,D]]: right from A -> B
            #         (innermost ancestor must be the inner split, not root)
            t2,A2,B2,C2,D2=h.layout_apply_nested_hh()
            h.focus(C2); h.focus(A2)
            wait_state(state_dir, ws, t2, lambda st: st.get("last_focused_pane") == A2)
            ae6=dict(h.env); ae6.update({"HERDR_PLUGIN_STATE_DIR":str(state_dir),
                 "HERDR_PANE_ID":A2,"HERDR_TAB_ID":t2,"HERDR_WORKSPACE_ID":ws})
            run_action(ae6,"right")
            f=h.focused_pane(); check("nested-hh right from A -> B (innermost)", f==B2, f"(got {f}, want {B2})")
            # and right from B (no horizontal ancestor inside inner split) -> falls
            # back to default: root split sibling subtree [C,D] first leaf C2
            h.focus(B2)
            ae6b=dict(ae6); ae6b["HERDR_PANE_ID"]=B2
            run_action(ae6b,"right")
            f=h.focused_pane(); check("nested-hh right from B -> default (C leaf)", f==C2, f"(got {f}, want {C2})")

            # --- Scenario 7: action with daemon down & no state -> fallback default nav
            dproc.terminate(); dproc.wait(timeout=5)
            shutil.rmtree(state_dir, ignore_errors=True); state_dir.mkdir()
            # daemon still holds flock file; remove so a new one could start, but we won't.
            action_env_no=dict(action_env_base); action_env_no["HERDR_PLUGIN_STATE_DIR"]=str(state_dir)
            action_env_no["HERDR_PANE_ID"]=A
            r=run_action(action_env_no,"right")
            f=h.focused_pane()
            # no smart state: right from A should fall to default -> B (first leaf) anyway
            check("daemon down -> default right from A", f in (B,C) and r.returncode==0, f"(got {f}, rc={r.returncode})")

            print(f"\n{'ALL PASS' if failures==0 else f'{failures} FAILURE(S)'}")
            return 0 if failures==0 else 1
        finally:
            try: dproc.terminate(); dproc.wait(timeout=3)
            except Exception:
                try: dproc.kill()
                except Exception: pass
    finally:
        h.stop()
        if keep: print(f"kept test dir: {tmp}")
        else:
            shutil.rmtree(tmp, ignore_errors=True)

if __name__=="__main__":
    sys.exit(main())