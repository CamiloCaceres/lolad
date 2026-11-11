package dev.caceres.lolad;

import android.app.Activity;
import android.app.Dialog;
import android.graphics.Bitmap;
import android.graphics.Color;
import android.graphics.Matrix;
import android.graphics.drawable.ColorDrawable;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.ScaleGestureDetector;
import android.view.View;
import android.view.ViewGroup;
import android.view.Window;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import com.google.android.material.button.MaterialButton;
import android.content.res.ColorStateList;

/**
 * Full-screen preview for a clean image attachment: pinch, double-tap and drag.
 * Bytes arrive through the same cached, off-main-thread path as the thumbnail;
 * nothing is decoded or executed on the UI thread.
 */
final class ImagePreview {
  private ImagePreview() {}

  static void show(
      Activity activity,
      Ui ui,
      ImageCache images,
      Api api,
      String peer,
      Attachment attachment,
      Runnable download) {
    Dialog dialog = new Dialog(activity);
    dialog.requestWindowFeature(Window.FEATURE_NO_TITLE);
    FrameLayout surface = new FrameLayout(activity);
    surface.setBackgroundColor(Color.parseColor("#0D1117"));

    LinearLayout column = new LinearLayout(activity);
    column.setOrientation(LinearLayout.VERTICAL);
    column.setPadding(ui.dp(12), ui.dp(12), ui.dp(12), ui.dp(12));
    surface.addView(
        column,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

    LinearLayout header = new LinearLayout(activity);
    header.setOrientation(LinearLayout.HORIZONTAL);
    header.setGravity(Gravity.CENTER_VERTICAL);
    TextView title =
        text(ui, attachment.shortName(), Ui.LABEL, Color.parseColor("#E7EBF1"), 500);
    title.setMaxLines(1);
    title.setEllipsize(android.text.TextUtils.TruncateAt.END);
    header.addView(title, new LinearLayout.LayoutParams(0, -2, 1f));
    MaterialButton close =
        ui.icon(R.drawable.ic_close, "Close preview", dialog::dismiss);
    close.setIconTint(ColorStateList.valueOf(Color.parseColor("#E7EBF1")));
    header.addView(close);
    column.addView(header);

    FrameLayout stage = new FrameLayout(activity);
    column.addView(stage, new LinearLayout.LayoutParams(-1, 0, 1f));

    Zoomable image = new Zoomable(activity);
    image.setScaleType(ImageView.ScaleType.MATRIX);
    stage.addView(
        image,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

    TextView loading =
        text(ui, "Loading preview…", Ui.LABEL, Color.parseColor("#B4BECC"), 400);
    loading.setGravity(Gravity.CENTER);
    stage.addView(
        loading,
        new FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

    LinearLayout footer = new LinearLayout(activity);
    footer.setOrientation(LinearLayout.HORIZONTAL);
    footer.setGravity(Gravity.CENTER_VERTICAL);
    TextView size =
        text(ui, attachment.sizeLabel() + " · " + attachment.badge(), Ui.METADATA, Color.parseColor("#97A4B4"), 400);
    footer.addView(size, new LinearLayout.LayoutParams(0, -2, 1f));
    MaterialButton save =
        new MaterialButton(
            activity, null, com.google.android.material.R.attr.materialButtonOutlinedStyle);
    save.setText("Download");
    save.setAllCaps(false);
    save.setTextSize(TypedValue.COMPLEX_UNIT_SP, Ui.ACTION);
    save.setMinHeight(ui.dp(48));
    save.setInsetTop(0);
    save.setInsetBottom(0);
    save.setCornerRadius(ui.dp(Ui.BUTTON_RADIUS));
    save.setTextColor(Color.parseColor("#E7EBF1"));
    save.setStrokeColor(ColorStateList.valueOf(Color.parseColor("#3A4656")));
    save.setBackgroundTintList(ColorStateList.valueOf(Color.parseColor("#2C3642")));
    save.setOnClickListener(
        v -> {
          dialog.dismiss();
          download.run();
        });
    footer.addView(save);
    column.addView(footer);

    int target = Math.max(
        activity.getResources().getDisplayMetrics().widthPixels,
        activity.getResources().getDisplayMetrics().heightPixels);
    if (attachment.localUri != null) {
      images.loadLocal(
          attachment.cacheKey(),
          android.net.Uri.parse(attachment.localUri),
          target,
          (key, bitmap) -> place(loading, image, bitmap));
    } else {
      images.loadRemote(
          api,
          peer,
          attachment,
          target,
          true,
          (key, bitmap) -> {
            if (bitmap == null) {
              loading.setText("This preview couldn’t be loaded. Download it instead.");
              return;
            }
            place(loading, image, bitmap);
          });
    }

    dialog.setContentView(surface);
    Window window = dialog.getWindow();
    if (window != null) {
      window.setBackgroundDrawable(new ColorDrawable(Color.parseColor("#0D1117")));
      window.setLayout(
          ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT);
    }
    dialog.show();
    Window shown = dialog.getWindow();
    if (shown != null)
      shown.setLayout(
          ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT);
  }

  private static void place(TextView loading, Zoomable image, Bitmap bitmap) {
    loading.setVisibility(View.GONE);
    image.setImageBitmap(bitmap);
  }

  private static TextView text(
      Ui ui, CharSequence value, float size, int color, int weight) {
    TextView view = new TextView(ui.context);
    view.setText(value);
    view.setTextSize(TypedValue.COMPLEX_UNIT_SP, size);
    view.setTextColor(color);
    view.setTypeface(
        android.graphics.Typeface.create(
            weight >= 600 ? "sans-serif" : "sans-serif-medium",
            weight >= 700 ? android.graphics.Typeface.BOLD : android.graphics.Typeface.NORMAL));
    return view;
  }

  /** Pinch to zoom, drag to pan, double-tap to toggle fit. */
  static final class Zoomable extends androidx.appcompat.widget.AppCompatImageView {
    private final Matrix matrix = new Matrix();
    private final Matrix base = new Matrix();
    private final ScaleGestureDetector scale;
    private float scaleFactor = 1f;
    private float panX, panY;
    private float lastX, lastY;
    private boolean dragging;

    @android.annotation.SuppressLint("ClickableViewAccessibility")
    Zoomable(Activity activity) {
      super(activity);
      scale =
          new ScaleGestureDetector(
              activity,
              new ScaleGestureDetector.SimpleOnScaleGestureListener() {
                @Override
                public boolean onScale(ScaleGestureDetector detector) {
                  scaleFactor = Math.max(1f, Math.min(8f, scaleFactor * detector.getScaleFactor()));
                  clampPan();
                  apply();
                  return true;
                }
              });
      setOnTouchListener(
          (v, event) -> {
            scale.onTouchEvent(event);
            switch (event.getActionMasked()) {
              case MotionEvent.ACTION_DOWN:
                lastX = event.getX();
                lastY = event.getY();
                dragging = true;
                break;
              case MotionEvent.ACTION_POINTER_DOWN:
                dragging = false;
                break;
              case MotionEvent.ACTION_MOVE:
                if (dragging && scaleFactor > 1f) {
                  panX += event.getX() - lastX;
                  panY += event.getY() - lastY;
                  lastX = event.getX();
                  lastY = event.getY();
                  clampPan();
                  apply();
                }
                break;
              case MotionEvent.ACTION_UP:
                dragging = false;
                break;
              default:
                break;
            }
            return false;
          });
      setOnClickListener(
          v -> {
            scaleFactor = scaleFactor > 1.5f ? 1f : 2.5f;
            panX = 0;
            panY = 0;
            apply();
          });
    }

    @Override
    public void setImageBitmap(Bitmap bitmap) {
      super.setImageBitmap(bitmap);
      scaleFactor = 1f;
      panX = 0;
      panY = 0;
      setContentDescription("Image preview, pinch to zoom");
      post(this::fit);
    }

    @Override
    protected void onSizeChanged(int width, int height, int oldWidth, int oldHeight) {
      super.onSizeChanged(width, height, oldWidth, oldHeight);
      fit();
    }

    private void fit() {
      android.graphics.drawable.Drawable drawable = getDrawable();
      if (drawable == null || getWidth() == 0 || getHeight() == 0) return;
      float width = drawable.getIntrinsicWidth();
      float height = drawable.getIntrinsicHeight();
      if (width <= 0 || height <= 0) return;
      float fit = Math.min(getWidth() / width, getHeight() / height);
      base.setScale(fit, fit);
      base.postTranslate((getWidth() - width * fit) / 2f, (getHeight() - height * fit) / 2f);
      apply();
    }

    private void apply() {
      matrix.set(base);
      matrix.postScale(scaleFactor, scaleFactor, getWidth() / 2f, getHeight() / 2f);
      matrix.postTranslate(panX, panY);
      setImageMatrix(matrix);
    }

    private void clampPan() {
      if (scaleFactor <= 1f) {
        panX = 0;
        panY = 0;
        return;
      }
      float maxX = getWidth() * (scaleFactor - 1f) / 2f;
      float maxY = getHeight() * (scaleFactor - 1f) / 2f;
      panX = Math.max(-maxX, Math.min(maxX, panX));
      panY = Math.max(-maxY, Math.min(maxY, panY));
    }
  }
}
