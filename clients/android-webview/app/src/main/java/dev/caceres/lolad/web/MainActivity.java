package dev.caceres.lolad.web;

import androidx.activity.ComponentActivity;
import androidx.core.graphics.Insets;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowInsetsCompat;
import java.util.Locale;
import androidx.activity.OnBackPressedCallback;
import android.app.DownloadManager;
import android.content.ActivityNotFoundException;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Bundle;
import android.os.Environment;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

/**
 * A thin shell around the lolad dashboard. The dashboard already is the phone UI
 * (inbox, agents, composer); this only provides a window, a server setting, file
 * upload/download and navigation limits. It has no JavaScript bridge and keeps
 * the WebView confined to the configured HTTPS origin.
 */
public class MainActivity extends ComponentActivity {
    private static final int FILE_CHOOSER = 1;
    private static final String PREFS = "lolad";
    private static final String KEY_SERVER = "server";

    private FrameLayout root;
    private WebView web;
    private LinearLayout overlay;
    private TextView overlayText;
    private Button primary;
    private Button secondary;
    private ValueCallback<Uri[]> pendingChooser;
    private String server;       // https origin, no trailing slash
    private boolean pageFailed;

    @Override protected void onCreate(Bundle state) {
        super.onCreate(state);
        root = new FrameLayout(this);
        ViewCompat.setOnApplyWindowInsetsListener(root, (v, in) -> {
            Insets bars = in.getInsets(WindowInsetsCompat.Type.systemBars() | WindowInsetsCompat.Type.ime());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            return WindowInsetsCompat.CONSUMED;
        });
        setContentView(root);
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override public void handleOnBackPressed() {
                if (overlay.getVisibility() == View.VISIBLE && web.getUrl() != null) { overlay.setVisibility(View.GONE); return; }
                if (web.canGoBack()) { web.goBack(); return; }
                setEnabled(false);
                getOnBackPressedDispatcher().onBackPressed();
            }
        });
        buildWebView();
        buildOverlay();
        server = prefs().getString(KEY_SERVER, null);
        handle(getIntent(), state == null);
        if (state != null) web.restoreState(state);
    }

    @Override protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handle(intent, true);
    }

    private void handle(Intent intent, boolean load) {
        Uri data = intent == null ? null : intent.getData();
        if (data != null && "lolad".equals(data.getScheme()) && "pair".equals(data.getHost())) {
            String u = normalize(data.getQueryParameter("u"));
            if (u != null) {
                server = u;
                prefs().edit().putString(KEY_SERVER, u).apply();
                Toast.makeText(this, "Enter the pairing code on the Sign in page", Toast.LENGTH_LONG).show();
            }
        } else if (intent != null && "dev.caceres.lolad.web.SETTINGS".equals(intent.getAction())) {
            showServerForm(null);
            return;
        }
        if (!load) return;
        if (server == null) showServerForm(null); else open(server + "/");
    }

    private SharedPreferences prefs() { return getSharedPreferences(PREFS, MODE_PRIVATE); }

    /** Accept only a plain https origin (the tailnet address Tailscale Serve provides). */
    static String normalize(String raw) {
        if (raw == null) return null;
        String s = raw.trim();
        if (s.isEmpty()) return null;
        if (!s.contains("://")) s = "https://" + s;
        Uri u = Uri.parse(s);
        if (!"https".equalsIgnoreCase(u.getScheme()) || u.getHost() == null || u.getHost().isEmpty()) return null;
        if (u.getUserInfo() != null) return null;
        return "https://" + u.getHost().toLowerCase(Locale.ROOT) + (u.getPort() > 0 ? ":" + u.getPort() : "");
    }

    private boolean sameOrigin(Uri u) {
        return server != null && u.getScheme() != null && normalize(u.getScheme() + "://" + u.getAuthority()) != null
                && server.equals(normalize(u.getScheme() + "://" + u.getAuthority()));
    }

    private void buildWebView() {
        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        s.setSupportMultipleWindows(false);
        s.setMediaPlaybackRequiresUserGesture(true);
        CookieManager.getInstance().setAcceptCookie(true);
        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                Uri u = r.getUrl();
                if (sameOrigin(u)) return false;
                String sc = u.getScheme();
                if ("http".equals(sc) || "https".equals(sc)) {
                    try { startActivity(new Intent(Intent.ACTION_VIEW, u)); } catch (ActivityNotFoundException ignored) { }
                }
                return true;
            }
            @Override public void onPageStarted(WebView v, String url, Bitmap f) { pageFailed = false; }
            @Override public void onPageFinished(WebView v, String url) {
                if (!pageFailed) overlay.setVisibility(View.GONE);
                CookieManager.getInstance().flush();
            }
            @Override public void onReceivedError(WebView v, WebResourceRequest r, WebResourceError e) {
                if (!r.isForMainFrame()) return;
                pageFailed = true;
                showError("Can't reach " + server + "\n\n" + e.getDescription() + " (code " + e.getErrorCode() + ")\n\nIs Tailscale on, and is lolad running on your Mac?");
            }
            @Override public void onReceivedHttpError(WebView v, WebResourceRequest r, android.webkit.WebResourceResponse e) {
                if (r.isForMainFrame() && e.getStatusCode() >= 500) {
                    pageFailed = true;
                    showError("The server answered " + e.getStatusCode() + ". Tailscale Serve may be up while lolad is down.");
                }
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            @Override public boolean onShowFileChooser(WebView v, ValueCallback<Uri[]> cb, FileChooserParams p) {
                if (pendingChooser != null) pendingChooser.onReceiveValue(null);
                pendingChooser = cb;
                try {
                    startActivityForResult(p.createIntent(), FILE_CHOOSER);
                } catch (ActivityNotFoundException e) {
                    pendingChooser = null;
                    return false;
                }
                return true;
            }
        });
        web.setDownloadListener((url, ua, disp, mime, len) -> {
            Uri u = Uri.parse(url);
            if (!sameOrigin(u)) return; // never hand the session cookie to another host
            try {
                DownloadManager.Request req = new DownloadManager.Request(u);
                req.addRequestHeader("Cookie", CookieManager.getInstance().getCookie(url));
                req.addRequestHeader("User-Agent", ua);
                String name = URLUtil.guessFileName(url, disp, mime);
                req.setMimeType(mime);
                req.setTitle(name);
                req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
                req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
                ((DownloadManager) getSystemService(Context.DOWNLOAD_SERVICE)).enqueue(req);
                Toast.makeText(this, "Downloading " + name, Toast.LENGTH_SHORT).show();
            } catch (RuntimeException e) {
                Toast.makeText(this, "Download failed", Toast.LENGTH_SHORT).show();
            }
        });
        root.addView(web, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    private void buildOverlay() {
        overlay = new LinearLayout(this);
        overlay.setOrientation(LinearLayout.VERTICAL);
        overlay.setGravity(Gravity.CENTER);
        overlay.setBackgroundColor(0xFF111111);
        int p = (int) (24 * getResources().getDisplayMetrics().density);
        overlay.setPadding(p, p, p, p);
        overlayText = new TextView(this);
        overlayText.setTextColor(0xFFEEEEEE);
        overlayText.setTextSize(16);
        overlayText.setGravity(Gravity.CENTER);
        overlay.addView(overlayText);
        primary = new Button(this);
        secondary = new Button(this);
        overlay.addView(primary);
        overlay.addView(secondary);
        overlay.setVisibility(View.GONE);
        root.addView(overlay, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    private void showError(String msg) {
        overlayText.setText(msg);
        primary.setText("Retry");
        primary.setOnClickListener(v -> { overlay.setVisibility(View.GONE); open(server + "/"); });
        secondary.setText("Change server");
        secondary.setVisibility(View.VISIBLE);
        secondary.setOnClickListener(v -> showServerForm(null));
        overlay.removeAllViews();
        overlay.addView(overlayText);
        overlay.addView(primary);
        overlay.addView(secondary);
        overlay.setVisibility(View.VISIBLE);
    }

    private void showServerForm(String error) {
        overlay.removeAllViews();
        TextView t = new TextView(this);
        t.setTextColor(0xFFEEEEEE);
        t.setTextSize(16);
        t.setText(error != null ? error : "Your lolad address on the tailnet,\nfor example my-mac.my-tailnet.ts.net:4100");
        t.setGravity(Gravity.CENTER);
        EditText in = new EditText(this);
        in.setHint("https://my-mac.my-tailnet.ts.net:4100");
        in.setHintTextColor(0xFF888888);
        in.setTextColor(0xFFFFFFFF);
        in.setSingleLine();
        in.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        in.setImeOptions(EditorInfo.IME_ACTION_GO);
        if (server != null) in.setText(server);
        Button go = new Button(this);
        go.setText("Connect");
        Runnable connect = () -> {
            String n = normalize(in.getText().toString());
            if (n == null) { showServerForm("Enter an https address. Tailscale Serve provides HTTPS."); return; }
            server = n;
            prefs().edit().putString(KEY_SERVER, n).apply();
            overlay.setVisibility(View.GONE);
            open(n + "/");
        };
        go.setOnClickListener(v -> connect.run());
        in.setOnEditorActionListener((v, a, e) -> { connect.run(); return true; });
        overlay.addView(t);
        overlay.addView(in, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        overlay.addView(go);
        overlay.setVisibility(View.VISIBLE);
    }

    private void open(String url) { web.loadUrl(url); }

    @Override protected void onActivityResult(int req, int res, Intent data) {
        if (req == FILE_CHOOSER && pendingChooser != null) {
            pendingChooser.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(res, data));
            pendingChooser = null;
            return;
        }
        super.onActivityResult(req, res, data);
    }

    @Override protected void onSaveInstanceState(Bundle out) { super.onSaveInstanceState(out); web.saveState(out); }
    @Override protected void onPause() { CookieManager.getInstance().flush(); web.onPause(); super.onPause(); }
    @Override protected void onResume() { super.onResume(); web.onResume(); }
    @Override protected void onDestroy() { root.removeView(web); web.destroy(); super.onDestroy(); }
}
