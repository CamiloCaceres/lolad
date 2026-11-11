package dev.caceres.lolad;

import android.content.Context;
import android.content.res.ColorStateList;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.core.graphics.ColorUtils;
import androidx.core.view.ViewCompat;
import com.google.android.material.button.MaterialButton;
import com.google.android.material.shape.ShapeAppearanceModel;

/**
 * Shared visual language: pearl-gray paper, charcoal ink, hairline lines and a
 * compact native type hierarchy. Maestro Lite's macOS roles map to Material 3
 * tonal surfaces here; there is no blur or glass.
 */
final class Ui {
  static final float ORGANIZATION = 27;
  static final float TITLE = 23;
  static final float HEADLINE = 21;
  static final float BODY = 16;
  static final float ACTION = 14;
  static final float LABEL = 13;
  static final float METADATA = 12;
  static final float TERMINAL = 13;

  static final int CONTENT_RADIUS = 24;
  static final int ROW_RADIUS = 15;
  static final int BUTTON_RADIUS = 14;
  static final int FIELD_RADIUS = 12;
  static final int TERMINAL_RADIUS = 16;
  static final int SEGMENT_RADIUS = 10;

  final Context context;
  final float density;
  final int hairlinePx;
  final int ink, paper, backdrop, secondary, muted, outline, hairline, quiet, onQuiet, track;
  final int awake, attention, error, raised, terminal, terminalInk;
  final int mine, onMine, cardMine, cardMineHairline;

  Ui(Context context) {
    this.context = context;
    density = context.getResources().getDisplayMetrics().density;
    hairlinePx = Math.max(1, Math.round(density * 0.75f));
    ink = attr(com.google.android.material.R.attr.colorOnSurface);
    paper = attr(com.google.android.material.R.attr.colorSurface);
    secondary = attr(com.google.android.material.R.attr.colorOnSurfaceVariant);
    outline = attr(com.google.android.material.R.attr.colorOutline);
    quiet = attr(com.google.android.material.R.attr.colorSecondaryContainer);
    track = context.getColor(R.color.lolad_track);
    onQuiet = attr(com.google.android.material.R.attr.colorOnSecondaryContainer);
    backdrop = context.getColor(R.color.lolad_backdrop);
    muted = context.getColor(R.color.lolad_muted);
    hairline = context.getColor(R.color.lolad_hairline);
    awake = context.getColor(R.color.lolad_awake);
    attention = context.getColor(R.color.lolad_attention);
    error = context.getColor(R.color.lolad_error);
    raised = context.getColor(R.color.lolad_raised);
    terminal = context.getColor(R.color.lolad_terminal);
    terminalInk = context.getColor(R.color.lolad_terminal_ink);
    mine = context.getColor(R.color.lolad_mine);
    onMine = context.getColor(R.color.lolad_on_mine);
    cardMine = context.getColor(R.color.lolad_card_mine);
    cardMineHairline = context.getColor(R.color.lolad_card_mine_hairline);
  }

  int dp(float value) {
    return Math.round(value * density);
  }

  int color(int resource) {
    return context.getColor(resource);
  }

  /** Transitions and thumbnail fades disappear when the system removes animations. */
  static boolean animationsEnabled() {
    try {
      return android.animation.ValueAnimator.areAnimatorsEnabled();
    } catch (Throwable ignored) {
      return true;
    }
  }

  int withAlpha(int color, int alpha) {
    return ColorUtils.setAlphaComponent(color, alpha);
  }

  Drawable pressable(Drawable content, int radiusDp) {
    GradientDrawable mask = surface(Color.WHITE, radiusDp);
    return new RippleDrawable(ColorStateList.valueOf(withAlpha(ink, 0x22)), content, mask);
  }

  int attr(int attribute) {
    TypedValue value = new TypedValue();
    context.getTheme().resolveAttribute(attribute, value, true);
    return value.data;
  }

  private Typeface face(int weight) {
    if (weight >= 700) return Typeface.create("sans-serif", Typeface.BOLD);
    if (weight >= 500) return Typeface.create("sans-serif-medium", Typeface.NORMAL);
    return Typeface.create("sans-serif", Typeface.NORMAL);
  }

  TextView text(CharSequence value, float size, int color, int weight, float lineSpacing) {
    TextView view = new TextView(context);
    view.setText(value);
    view.setTextSize(TypedValue.COMPLEX_UNIT_SP, size);
    view.setTextColor(color);
    view.setTypeface(face(weight));
    view.setLineSpacing(dp(lineSpacing), 1f);
    return view;
  }

  TextView organization(String value) {
    TextView view = text(value, ORGANIZATION, ink, 500, 0);
    view.setLetterSpacing(-0.022f);
    return view;
  }

  TextView title(String value) {
    TextView view = text(value, TITLE, ink, 500, 0);
    view.setLetterSpacing(-0.017f);
    return view;
  }

  TextView headline(String value) {
    return text(value, HEADLINE, ink, 600, 0);
  }

  TextView rowTitle(String value) {
    return text(value, BODY + 1, ink, 600, 0);
  }

  TextView body(String value) {
    return text(value, BODY, ink, 400, 3);
  }

  TextView label(String value) {
    return text(value, LABEL, secondary, 500, 1);
  }

  TextView quiet(String value) {
    return text(value, METADATA, muted, 400, 1);
  }

  TextView mono(String value) {
    TextView view = text(value, TERMINAL, ink, 400, 2);
    view.setTypeface(Typeface.MONOSPACE);
    return view;
  }

  GradientDrawable surface(int color, int radiusDp) {
    GradientDrawable drawable = new GradientDrawable();
    drawable.setColor(color);
    drawable.setCornerRadius(dp(radiusDp));
    return drawable;
  }

  GradientDrawable outlined(int color, int radiusDp, int strokeColor) {
    GradientDrawable drawable = surface(color, radiusDp);
    drawable.setStroke(hairlinePx, strokeColor);
    return drawable;
  }

  View hairline() {
    View line = new View(context);
    line.setBackgroundColor(hairline);
    line.setLayoutParams(new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, hairlinePx));
    return line;
  }

  View dot(int color) {
    View dot = new View(context);
    GradientDrawable circle = new GradientDrawable();
    circle.setShape(GradientDrawable.OVAL);
    circle.setColor(color);
    dot.setBackground(circle);
    dot.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
    return dot;
  }

  LinearLayout status(String label, int color) {
    LinearLayout row = new LinearLayout(context);
    row.setOrientation(LinearLayout.HORIZONTAL);
    row.setGravity(Gravity.CENTER_VERTICAL);
    View dot = dot(color);
    LinearLayout.LayoutParams dotp = new LinearLayout.LayoutParams(dp(8), dp(8));
    dotp.setMarginEnd(dp(7));
    row.addView(dot, dotp);
    row.addView(text(label, LABEL, color, 500, 0));
    return row;
  }

  MaterialButton primary(String label, Runnable action) {
    MaterialButton button = baseButton(label, action);
    button.setBackgroundTintList(enabledColors(ink, quiet));
    button.setTextColor(enabledColors(paper, muted));
    return button;
  }

  MaterialButton quietAction(String label, Runnable action) {
    MaterialButton button = baseButton(label, action);
    button.setBackgroundTintList(enabledColors(quiet, track));
    button.setTextColor(enabledColors(ink, muted));
    return button;
  }

  MaterialButton textAction(String label, Runnable action) {
    MaterialButton button = baseButton(label, action);
    button.setBackgroundTintList(ColorStateList.valueOf(Color.TRANSPARENT));
    button.setTextColor(secondary);
    button.setStrokeWidth(0);
    return button;
  }

  MaterialButton icon(int drawable, String description, Runnable action) {
    MaterialButton button =
        new MaterialButton(
            context, null, com.google.android.material.R.attr.materialButtonOutlinedStyle);
    button.setText(null);
    button.setIconResource(drawable);
    button.setIconSize(dp(22));
    button.setIconPadding(0);
    button.setIconTint(enabledColors(secondary, muted));
    button.setIconGravity(MaterialButton.ICON_GRAVITY_TEXT_START);
    button.setBackgroundTintList(ColorStateList.valueOf(Color.TRANSPARENT));
    button.setStrokeWidth(0);
    button.setInsetTop(0);
    button.setInsetBottom(0);
    button.setMinWidth(dp(48));
    button.setMinimumWidth(dp(48));
    button.setMinHeight(dp(48));
    button.setMinimumHeight(dp(48));
    button.setPadding(0, 0, 0, 0);
    button.setContentDescription(description);
    if (action != null) button.setOnClickListener(v -> action.run());
    return button;
  }

  private MaterialButton baseButton(String label, Runnable action) {
    MaterialButton button =
        new MaterialButton(context, null, com.google.android.material.R.attr.materialButtonOutlinedStyle);
    button.setText(label);
    button.setAllCaps(false);
    button.setTextSize(TypedValue.COMPLEX_UNIT_SP, ACTION);
    button.setTypeface(face(600));
    button.setMinHeight(dp(48));
    button.setMinimumHeight(dp(48));
    button.setInsetTop(0);
    button.setInsetBottom(0);
    button.setCornerRadius(dp(BUTTON_RADIUS));
    button.setStrokeWidth(0);
    button.setShapeAppearanceModel(
        ShapeAppearanceModel.builder().setAllCornerSizes(dp(BUTTON_RADIUS)).build());
    button.setGravity(Gravity.CENTER);
    button.setPadding(dp(18), 0, dp(18), 0);
    if (action != null) button.setOnClickListener(v -> action.run());
    return button;
  }

  LinearLayout segmentTrack() {
    LinearLayout track = new LinearLayout(context);
    track.setOrientation(LinearLayout.HORIZONTAL);
    track.setBackground(surface(this.track, SEGMENT_RADIUS));
    track.setPadding(dp(4), dp(4), dp(4), dp(4));
    return track;
  }

  MaterialButton segment(String label, Runnable action) {
    MaterialButton button = textAction(label, action);
    button.setCornerRadius(dp(SEGMENT_RADIUS));
    button.setShapeAppearanceModel(
        ShapeAppearanceModel.builder().setAllCornerSizes(dp(SEGMENT_RADIUS)).build());
    return button;
  }

  void selectSegment(MaterialButton button, boolean selected) {
    button.setBackgroundTintList(ColorStateList.valueOf(selected ? paper : Color.TRANSPARENT));
    button.setTextColor(selected ? ink : secondary);
    button.setTypeface(face(selected ? 600 : 500));
    button.setSelected(selected);
    ViewCompat.setStateDescription(button, selected ? "Selected" : "Not selected");
  }

  private static ColorStateList enabledColors(int enabled, int disabled) {
    return new ColorStateList(
        new int[][] {new int[] {android.R.attr.state_enabled}, new int[] {}},
        new int[] {enabled, disabled});
  }
}
