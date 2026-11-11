package dev.caceres.lolad;

import android.graphics.drawable.GradientDrawable;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.annotation.NonNull;
import androidx.recyclerview.widget.DiffUtil;
import androidx.recyclerview.widget.ListAdapter;
import androidx.recyclerview.widget.RecyclerView;
import com.google.android.material.button.MaterialButton;

/** Agents rows with a muted status anchor and explicit state text. */
final class AgentAdapter extends ListAdapter<Model.Agent, AgentAdapter.Holder> {
  interface Open {
    void agent(String name);
  }

  private static final DiffUtil.ItemCallback<Model.Agent> DIFF =
      new DiffUtil.ItemCallback<Model.Agent>() {
        @Override
        public boolean areItemsTheSame(@NonNull Model.Agent oldItem, @NonNull Model.Agent newItem) {
          return oldItem.name.equals(newItem.name);
        }

        @Override
        public boolean areContentsTheSame(@NonNull Model.Agent oldItem, @NonNull Model.Agent newItem) {
          return oldItem.display.equals(newItem.display)
              && oldItem.stateLabel.equals(newItem.stateLabel)
              && oldItem.runtimeLine.equals(newItem.runtimeLine)
              && oldItem.lastActivity.equals(newItem.lastActivity)
              && oldItem.activity.equals(newItem.activity)
              && oldItem.openAsks == newItem.openAsks
              && oldItem.unread == newItem.unread
              && oldItem.tone.equals(newItem.tone);
        }
      };

  private final Ui ui;
  private final Open open;

  AgentAdapter(Ui ui, Open open) {
    super(DIFF);
    this.ui = ui;
    this.open = open;
    setHasStableIds(true);
  }

  @Override
  public long getItemId(int position) {
    return Model.stableId(getItem(position).name);
  }

  @NonNull
  @Override
  public Holder onCreateViewHolder(@NonNull ViewGroup parent, int viewType) {
    LinearLayout card = new LinearLayout(parent.getContext());
    card.setOrientation(LinearLayout.VERTICAL);
    card.setPadding(ui.dp(16), ui.dp(16), ui.dp(16), ui.dp(16));
    card.setBackground(
        ui.pressable(ui.outlined(ui.paper, Ui.ROW_RADIUS, ui.hairline), Ui.ROW_RADIUS));
    card.setClickable(true);
    RecyclerView.LayoutParams params =
        new RecyclerView.LayoutParams(
            RecyclerView.LayoutParams.MATCH_PARENT, RecyclerView.LayoutParams.WRAP_CONTENT);
    params.bottomMargin = ui.dp(12);
    card.setLayoutParams(params);

    TextView title = ui.rowTitle("");
    card.addView(title);

    LinearLayout status = new LinearLayout(parent.getContext());
    status.setOrientation(LinearLayout.HORIZONTAL);
    status.setGravity(Gravity.CENTER_VERTICAL);
    status.setPadding(0, ui.dp(6), 0, 0);
    View dot = ui.dot(ui.muted);
    LinearLayout.LayoutParams dotParams = new LinearLayout.LayoutParams(ui.dp(8), ui.dp(8));
    dotParams.setMarginEnd(ui.dp(7));
    status.addView(dot, dotParams);
    TextView state = ui.text("", Ui.LABEL, ui.secondary, 500, 0);
    status.addView(state);
    card.addView(status);

    TextView needs = ui.text("", Ui.LABEL, ui.attention, 500, 1);
    needs.setVisibility(View.GONE);
    needs.setPadding(0, ui.dp(4), 0, 0);
    card.addView(needs);

    TextView unread = ui.label("");
    unread.setVisibility(View.GONE);
    unread.setPadding(0, ui.dp(4), 0, 0);
    card.addView(unread);

    TextView runtime = ui.quiet("");
    runtime.setPadding(0, ui.dp(7), 0, 0);
    card.addView(runtime);

    TextView activity = ui.quiet("");
    activity.setPadding(0, ui.dp(2), 0, 0);
    card.addView(activity);

    MaterialButton openButton = ui.quietAction("View agent", null);
    LinearLayout.LayoutParams buttonParams =
        new LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
    buttonParams.topMargin = ui.dp(8);
    openButton.setLayoutParams(buttonParams);
    card.addView(openButton);

    return new Holder(card, title, dot, state, needs, unread, runtime, activity, openButton);
  }

  @Override
  public void onBindViewHolder(@NonNull Holder holder, int position) {
    Model.Agent row = getItem(position);
    holder.title.setText(row.display);
    holder.state.setText(row.stateLabel);
    int color = color(row.tone);
    holder.dot.setBackground(circle(color));
    holder.state.setTextColor(color);
    if (row.openAsks > 0) {
      holder.needs.setVisibility(View.VISIBLE);
      holder.needs.setText(ConversationAdapter.questions(row.openAsks));
    } else {
      holder.needs.setVisibility(View.GONE);
    }
    if (row.unread > 0) {
      holder.unread.setVisibility(View.VISIBLE);
      holder.unread.setText(row.unread + " unread");
    } else {
      holder.unread.setVisibility(View.GONE);
    }
    holder.runtime.setText(row.runtimeLine);
    holder.activity.setText(row.activity);
    holder.itemView.setOnClickListener(v -> open.agent(row.name));
    holder.open.setOnClickListener(v -> open.agent(row.name));
    holder.open.setContentDescription("View " + row.display);
  }

  private int color(Model.Tone tone) {
    switch (tone) {
      case AWAKE:
        return ui.awake;
      case ATTENTION:
        return ui.attention;
      case ERROR:
        return ui.error;
      default:
        return ui.muted;
    }
  }

  private GradientDrawable circle(int color) {
    GradientDrawable drawable = new GradientDrawable();
    drawable.setShape(GradientDrawable.OVAL);
    drawable.setColor(color);
    return drawable;
  }

  static final class Holder extends RecyclerView.ViewHolder {
    final TextView title, state, needs, unread, runtime, activity;
    final View dot;
    final MaterialButton open;

    Holder(
        View item,
        TextView title,
        View dot,
        TextView state,
        TextView needs,
        TextView unread,
        TextView runtime,
        TextView activity,
        MaterialButton open) {
      super(item);
      this.title = title;
      this.dot = dot;
      this.state = state;
      this.needs = needs;
      this.unread = unread;
      this.runtime = runtime;
      this.activity = activity;
      this.open = open;
    }
  }
}
