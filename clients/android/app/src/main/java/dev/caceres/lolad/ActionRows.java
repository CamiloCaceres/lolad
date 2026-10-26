package dev.caceres.lolad;

import android.content.Context;
import android.view.View;
import android.view.ViewGroup;

/** Native controls wrap instead of hiding recovery actions offscreen. */
final class ActionRows extends ViewGroup {
  private final int gap;

  ActionRows(Context context, int gap) {
    super(context);
    this.gap = gap;
  }

  @Override
  protected void onMeasure(int widthSpec, int heightSpec) {
    int width = MeasureSpec.getSize(widthSpec);
    int available = Math.max(0, width - getPaddingLeft() - getPaddingRight());
    int used = 0, rowHeight = 0, height = getPaddingTop() + getPaddingBottom();
    for (int i = 0; i < getChildCount(); i++) {
      View child = getChildAt(i);
      if (child.getVisibility() == GONE) continue;
      child.measure(
          MeasureSpec.makeMeasureSpec(available, MeasureSpec.AT_MOST),
          MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
      int childWidth = child.getMeasuredWidth();
      if (used > 0 && used + childWidth > available) {
        height += rowHeight + gap;
        used = 0;
        rowHeight = 0;
      }
      used += childWidth + gap;
      rowHeight = Math.max(rowHeight, child.getMeasuredHeight());
    }
    setMeasuredDimension(
        resolveSize(width, widthSpec), resolveSize(height + rowHeight, heightSpec));
  }

  @Override
  protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
    int available = right - left - getPaddingLeft() - getPaddingRight();
    int used = 0, y = getPaddingTop(), rowHeight = 0;
    boolean rtl = getLayoutDirection() == LAYOUT_DIRECTION_RTL;
    for (int i = 0; i < getChildCount(); i++) {
      View child = getChildAt(i);
      if (child.getVisibility() == GONE) continue;
      int width = child.getMeasuredWidth(), height = child.getMeasuredHeight();
      if (used > 0 && used + width > available) {
        y += rowHeight + gap;
        used = 0;
        rowHeight = 0;
      }
      int x = rtl ? right - left - getPaddingRight() - used - width : getPaddingLeft() + used;
      child.layout(x, y, x + width, y + height);
      used += width + gap;
      rowHeight = Math.max(rowHeight, height);
    }
  }
}
