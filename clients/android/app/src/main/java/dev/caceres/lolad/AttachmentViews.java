package dev.caceres.lolad;

import android.content.Context;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.TextView;
import com.google.android.material.button.MaterialButton;

/**
 * Attachment cards and upload chips. Cards carry a ripple and an explicit
 * action sentence; thumbnails are always loaded off the main thread and a
 * recycled row's late result is dropped by matching its tag.
 */
final class AttachmentViews {
  interface Events {
    void attachment(Attachment attachment, View source);
  }

  private AttachmentViews() {}

  static View messageCard(
      Context context,
      Ui ui,
      ImageCache images,
      Api api,
      String peer,
      boolean mine,
      Attachment attachment,
      Events events,
      int targetPx,
      java.util.List<String> imageKeys) {
    LinearLayout card = new LinearLayout(context);
    card.setOrientation(LinearLayout.VERTICAL);
    card.setPadding(ui.dp(10), ui.dp(10), ui.dp(10), ui.dp(10));
    int cardColor = mine ? ui.cardMine : ui.quiet;
    int hairline = mine ? ui.cardMineHairline : ui.hairline;
    int textColor = mine ? ui.onMine : ui.ink;
    card.setBackground(ui.pressable(ui.outlined(cardColor, 12, hairline), 12));
    card.setClickable(true);
    card.setFocusable(true);
    card.setContentDescription(
        (attachment.canInline() ? "Preview " : "Download ")
            + attachment.filename
            + ", "
            + attachment.sizeLabel()
            + (attachment.suspicious() ? ", flagged as suspicious" : ""));

    if (attachment.canInline()) {
      FrameLayout frame = new FrameLayout(context);
      frame.setBackground(ui.surface(mine ? ui.mine : ui.paper, 8));
      frame.setClipToOutline(true);
      card.addView(
          frame,
          new LinearLayout.LayoutParams(
              LinearLayout.LayoutParams.MATCH_PARENT, ui.dp(148)));

      TextView placeholder = ui.text("Loading preview…", Ui.METADATA, ui.secondary, 400, 0);
      placeholder.setGravity(Gravity.CENTER);
      frame.addView(
          placeholder,
          new FrameLayout.LayoutParams(
              FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

      ImageView image = new ImageView(context);
      image.setScaleType(ImageView.ScaleType.CENTER_CROP);
      image.setAlpha(0f);
      frame.addView(
          image,
          new FrameLayout.LayoutParams(
              FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

      String key = ImageCache.key(attachment.cacheKey(), targetPx);
      image.setTag(key);
      if (imageKeys != null) imageKeys.add(key);
      Api client = api;
      String conversation = peer;
      if (attachment.localUri != null) {
        images.loadLocal(
            attachment.cacheKey(),
            android.net.Uri.parse(attachment.localUri),
            targetPx,
            (loadedKey, bitmap) -> {
              if (!key.equals(image.getTag())) return;
              if (bitmap == null) return;
              placeholder.setVisibility(View.GONE);
              image.setImageBitmap(bitmap);
              reveal(image, ui);
            });
      } else if (client != null && conversation != null) {
        images.loadRemote(
            client,
            conversation,
            attachment,
            targetPx,
            false,
            (loadedKey, bitmap) -> {
              if (!key.equals(image.getTag())) return;
              if (bitmap == null) {
                placeholder.setText("Preview unavailable · tap to download");
                return;
              }
              placeholder.setVisibility(View.GONE);
              image.setImageBitmap(bitmap);
              reveal(image, ui);
            });
      } else {
        placeholder.setText("Preview unavailable · tap to download");
      }
    }

    LinearLayout caption = new LinearLayout(context);
    caption.setOrientation(LinearLayout.HORIZONTAL);
    caption.setGravity(Gravity.CENTER_VERTICAL);
    int top = attachment.canInline() ? ui.dp(8) : 0;
    caption.setPadding(0, top, 0, 0);

    if (!attachment.canInline()) {
      ImageView icon = new ImageView(context);
      icon.setImageResource(R.drawable.ic_file);
      icon.setColorFilter(textColor);
      icon.setContentDescription(AttachmentViews.badge(attachment.filename) + " file");
      icon.setBackground(ui.outlined(mine ? ui.mine : ui.paper, 8, hairline));
      icon.setPadding(ui.dp(11), ui.dp(11), ui.dp(11), ui.dp(11));
      LinearLayout.LayoutParams badgeParams = new LinearLayout.LayoutParams(ui.dp(44), ui.dp(44));
      badgeParams.setMarginEnd(ui.dp(10));
      caption.addView(icon, badgeParams);
    }

    LinearLayout words = new LinearLayout(context);
    words.setOrientation(LinearLayout.VERTICAL);
    TextView name = ui.text(attachment.shortName(), Ui.ACTION, textColor, 500, 0);
    name.setMaxLines(1);
    name.setEllipsize(TextUtils.TruncateAt.END);
    words.addView(name);
    String type = AttachmentViews.badge(attachment.filename);
    TextView detail =
        ui.text(
            type.isEmpty() ? attachment.sizeLabel() : type + " · " + attachment.sizeLabel(),
            Ui.METADATA,
            mine ? ui.onMine : ui.muted,
            400,
            0);
    detail.setAlpha(mine ? 0.75f : 1f);
    detail.setPadding(0, ui.dp(2), 0, 0);
    words.addView(detail);
    if (attachment.suspicious()) {
      TextView flag = ui.text("Suspicious · download only", Ui.METADATA, ui.paper, 600, 0);
      flag.setBackground(ui.surface(ui.attention, 6));
      flag.setPadding(ui.dp(6), ui.dp(2), ui.dp(6), ui.dp(2));
      LinearLayout.LayoutParams flagParams = new LinearLayout.LayoutParams(-2, -2);
      flagParams.topMargin = ui.dp(6);
      flag.setLayoutParams(flagParams);
      words.addView(flag);
    }
    caption.addView(words, new LinearLayout.LayoutParams(0, -2, 1f));
    card.addView(caption);

    card.setOnClickListener(v -> events.attachment(attachment, v));
    return card;
  }

  static View composerChip(
      Context context, Ui ui, ImageCache images, Uploads.Upload upload, Runnable remove, Runnable retry) {
    LinearLayout chip = new LinearLayout(context);
    chip.setOrientation(LinearLayout.VERTICAL);
    chip.setBackground(ui.outlined(ui.raised, 12, ui.hairline));
    chip.setPadding(ui.dp(10), ui.dp(8), ui.dp(6), ui.dp(8));

    LinearLayout row = new LinearLayout(context);
    row.setOrientation(LinearLayout.HORIZONTAL);
    row.setGravity(Gravity.CENTER_VERTICAL);

    FrameLayout thumb = new FrameLayout(context);
    thumb.setBackground(ui.surface(ui.track, 8));
    thumb.setClipToOutline(true);
    thumb.setLayoutParams(new LinearLayout.LayoutParams(ui.dp(40), ui.dp(40)));
    TextView badge = ui.text(AttachmentViews.badge(upload.filename), Ui.METADATA - 1, ui.ink, 700, 0);
    badge.setGravity(Gravity.CENTER);
    thumb.addView(
        badge,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
    ImageView image = new ImageView(context);
    image.setScaleType(ImageView.ScaleType.CENTER_CROP);
    image.setAlpha(0f);
    thumb.addView(
        image,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
    if (Attachments.imageType(upload.contentType)) {
      String key = ImageCache.key(upload.uri.toString(), ui.dp(40));
      image.setTag(key);
      images.loadLocal(
          upload.uri.toString(),
          upload.uri,
          ui.dp(40),
          (loadedKey, bitmap) -> {
            if (!key.equals(image.getTag()) || bitmap == null) return;
            badge.setVisibility(View.GONE);
            image.setImageBitmap(bitmap);
            reveal(image, ui);
          });
    }
    LinearLayout.LayoutParams thumbParams = new LinearLayout.LayoutParams(ui.dp(40), ui.dp(40));
    thumbParams.setMarginEnd(ui.dp(10));
    row.addView(thumb, thumbParams);

    LinearLayout words = new LinearLayout(context);
    words.setOrientation(LinearLayout.VERTICAL);
    TextView name = ui.text(Attachments.middleName(upload.filename, 34), Ui.LABEL, ui.ink, 500, 0);
    name.setMaxLines(1);
    name.setEllipsize(TextUtils.TruncateAt.END);
    words.addView(name);
    String detail =
        upload.state == Uploads.State.FAILED
            ? upload.error
            : upload.size > 0
                ? Attachments.humanSize(upload.size) + " · " + Uploads.describe(upload)
                : Uploads.describe(upload);
    TextView status = ui.text(detail, Ui.METADATA, upload.state == Uploads.State.FAILED ? ui.error : ui.muted, 400, 0);
    status.setMaxLines(2);
    status.setEllipsize(TextUtils.TruncateAt.END);
    status.setPadding(0, ui.dp(2), 0, 0);
    words.addView(status);
    row.addView(words, new LinearLayout.LayoutParams(0, -2, 1f));

    if (upload.state == Uploads.State.FAILED) {
      MaterialButton again = ui.textAction("Retry", retry);
      again.setMinWidth(ui.dp(48));
      row.addView(again);
    }

    MaterialButton close =
        ui.icon(R.drawable.ic_close, "Remove " + upload.filename, remove);
    row.addView(close);

    chip.addView(row);

    if (upload.state == Uploads.State.UPLOADING) {
      ProgressBar progress = new ProgressBar(context, null, android.R.attr.progressBarStyleHorizontal);
      progress.setIndeterminate(upload.percent < 0);
      progress.setMax(100);
      progress.setProgress(Math.max(0, upload.percent));
      progress.setProgressTintList(android.content.res.ColorStateList.valueOf(ui.ink));
      progress.setProgressBackgroundTintList(android.content.res.ColorStateList.valueOf(ui.track));
      LinearLayout.LayoutParams params =
          new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, ui.dp(3));
      params.topMargin = ui.dp(8);
      chip.addView(progress, params);
    }

    chip.setContentDescription(
        upload.filename + ", " + (upload.state == Uploads.State.FAILED ? upload.error : Uploads.describe(upload)));
    return chip;
  }

  static String badge(String filename) {
    String label = Attachments.extensionLabel(filename);
    return label.isEmpty() ? "FILE" : label;
  }

  private static void reveal(View view, Ui ui) {
    if (!Ui.animationsEnabled()) {
      view.setAlpha(1f);
      return;
    }
    view.animate().alpha(1f).setDuration(120).start();
  }
}
