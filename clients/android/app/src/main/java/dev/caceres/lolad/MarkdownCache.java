package dev.caceres.lolad;

import android.content.Context;
import android.os.Handler;
import android.os.Looper;
import android.util.LruCache;
import androidx.annotation.NonNull;
import io.noties.markwon.AbstractMarkwonPlugin;
import io.noties.markwon.Markwon;
import io.noties.markwon.core.MarkwonTheme;
import io.noties.markwon.ext.strikethrough.StrikethroughPlugin;
import io.noties.markwon.ext.tables.TablePlugin;
import java.util.HashSet;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.function.Consumer;

/**
 * Renders Markdown off the main thread and keeps the result per message id so
 * a thousand-message thread never parses the same body twice. Binds only read
 * the cache; a miss shows plain text for one frame and then rebinds.
 */
final class MarkdownCache {
  private static final int MAX_BYTES = 12 * 1024 * 1024;

  private volatile Markwon incoming;
  private volatile Markwon outgoing;
  private final Context context;
  private final int incomingLink, outgoingLink;
  private final ExecutorService renderer;
  private final Handler main = new Handler(Looper.getMainLooper());
  private final LruCache<String, CharSequence> cache;
  private final Set<String> pending = new HashSet<>();
  private volatile Consumer<String> listener;

  MarkdownCache(Context context) {
    this.context = context;
    incomingLink = context.getColor(R.color.lolad_secondary);
    outgoingLink = context.getColor(R.color.lolad_paper);
    renderer =
        Executors.newFixedThreadPool(
            2,
            runnable -> {
              Thread thread = new Thread(runnable, "lolad-markdown");
              thread.setDaemon(true);
              return thread;
            });
    cache =
        new LruCache<>(MAX_BYTES) {
          @Override
          protected int sizeOf(String key, CharSequence value) {
            return value.length() * 2 + 128;
          }
        };
  }

  private Markwon markwon(boolean mine) {
    Markwon local = mine ? outgoing : incoming;
    if (local != null) return local;
    synchronized (this) {
      local = mine ? outgoing : incoming;
      if (local == null) {
        local = build(context, mine ? outgoingLink : incomingLink);
        if (mine) outgoing = local;
        else incoming = local;
      }
      return local;
    }
  }

  private static Markwon build(Context context, int linkColor) {
    return Markwon.builder(context)
        .usePlugin(TablePlugin.create(context))
        .usePlugin(StrikethroughPlugin.create())
        .usePlugin(
            new AbstractMarkwonPlugin() {
              @Override
              public void configureTheme(@NonNull MarkwonTheme.Builder builder) {
                builder.linkColor(linkColor);
              }
            })
        .build();
  }

  static String key(String id, String body, boolean mine) {
    return (mine ? "o:" : "i:") + id + ":" + body.length() + ":" + body.hashCode();
  }

  CharSequence cached(String key) {
    return cache.get(key);
  }

  void setListener(Consumer<String> listener) {
    this.listener = listener;
  }

  void request(String key, String body, boolean mine) {
    if (cache.get(key) != null) return;
    synchronized (pending) {
      if (!pending.add(key)) return;
    }
    renderer.execute(
        () -> {
          CharSequence rendered;
          try {
            rendered = markwon(mine).toMarkdown(body);
          } catch (Throwable failure) {
            rendered = body;
          }
          final CharSequence value = rendered == null ? body : rendered;
          main.post(
              () -> {
                cache.put(key, value);
                synchronized (pending) {
                  pending.remove(key);
                }
                Consumer<String> notify = listener;
                if (notify != null) notify.accept(key);
              });
        });
  }

  void prefetch(String key, String body, boolean mine) {
    request(key, body, mine);
  }

  void clear() {
    cache.evictAll();
  }

  void shutdown() {
    renderer.shutdownNow();
    cache.evictAll();
  }
}
