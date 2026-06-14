#!/usr/bin/env python3
"""Generate report charts from verification CSV artifacts."""

from __future__ import annotations

import csv
import html
import math
from pathlib import Path
from typing import Iterable, NamedTuple

try:
    from PIL import Image, ImageDraw, ImageFont
except ModuleNotFoundError as exc:
    raise SystemExit("Pillow is required to render PNG report charts.") from exc


ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / "verification_artifacts"
CHARTS = ARTIFACTS / "charts"

WIDTH = 1320
HEIGHT = 720
LEFT = 250
RIGHT = 80
TOP = 70
BOTTOM = 90
PLOT_WIDTH = WIDTH - LEFT - RIGHT
PLOT_HEIGHT = HEIGHT - TOP - BOTTOM

FONT_REGULAR_PATHS = (
    "/System/Library/Fonts/Supplemental/Arial.ttf",
    "/Library/Fonts/Arial.ttf",
    "/System/Library/Fonts/Helvetica.ttc",
)
FONT_BOLD_PATHS = (
    "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
    "/Library/Fonts/Arial Bold.ttf",
    "/System/Library/Fonts/Supplemental/Arial.ttf",
)


class SeriesChart(NamedTuple):
    stem: str
    title: str
    labels: list[str]
    values: list[float]
    tick_step: float
    positive_is_better: bool
    negative_side: str
    positive_side: str
    footnote: str


def read_csv(path: Path) -> list[dict[str, str]]:
    with path.open(newline="") as fh:
        return list(csv.DictReader(fh))


def pct_label(value: float) -> str:
    if abs(value - round(value)) < 1e-9:
        return f"{int(round(value))}%"
    return f"{value:.1f}%"


def value_label(value: float) -> str:
    return f"{value:+.2f}%"


def symmetric_ticks(values: Iterable[float], step: float) -> tuple[float, float, list[float]]:
    max_abs = max(abs(v) for v in values)
    bound = max(step, math.ceil(max_abs / step) * step)
    count = int(round((bound * 2) / step))
    ticks = [round(-bound + step * i, 10) for i in range(count + 1)]
    return -bound, bound, ticks


def x_for(value: float, min_value: float, max_value: float) -> float:
    return LEFT + (value - min_value) / (max_value - min_value) * PLOT_WIDTH


def bar_color(value: float, positive_is_better: bool) -> str:
    if abs(value) < 1e-9:
        return "#6b7280"
    good = value > 0 if positive_is_better else value < 0
    return "#16a34a" if good else "#dc2626"


def svg_text(
    x: float,
    y: float,
    text: str,
    *,
    size: int,
    fill: str = "#111827",
    anchor: str = "start",
    weight: str | None = None,
) -> str:
    weight_attr = f' font-weight="{weight}"' if weight else ""
    return (
        f'<text x="{x:.1f}" y="{y:.1f}" text-anchor="{anchor}" '
        f'font-family="Arial, Helvetica, sans-serif" font-size="{size}"'
        f'{weight_attr} fill="{fill}">{html.escape(text)}</text>'
    )


def render_svg(chart: SeriesChart) -> str:
    min_value, max_value, ticks = symmetric_ticks(chart.values, chart.tick_step)
    zero_x = x_for(0, min_value, max_value)
    row_gap = PLOT_HEIGHT / len(chart.values)
    bar_height = min(42.0, row_gap * 0.68)

    lines = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{WIDTH}" height="{HEIGHT}" viewBox="0 0 {WIDTH} {HEIGHT}">',
        f'<rect x="0" y="0" width="{WIDTH}" height="{HEIGHT}" fill="#ffffff"/>',
        svg_text(WIDTH / 2, 34, chart.title, size=24, anchor="middle", weight="700"),
    ]

    for tick in ticks:
        x = x_for(tick, min_value, max_value)
        stroke = "#d1d5db" if abs(tick) < 1e-9 else "#e5e7eb"
        width = "2" if abs(tick) < 1e-9 else "1"
        lines.append(f'<line x1="{x:.1f}" y1="{TOP}" x2="{x:.1f}" y2="{HEIGHT - BOTTOM}" stroke="{stroke}" stroke-width="{width}"/>')
        lines.append(svg_text(x, HEIGHT - 68, pct_label(tick), size=12, fill="#6b7280", anchor="middle"))

    lines.append(svg_text(LEFT, TOP - 10, chart.negative_side, size=12, fill=bar_color(-1, chart.positive_is_better), anchor="start", weight="700"))
    lines.append(svg_text(zero_x, TOP - 10, "C++ parity", size=12, fill="#374151", anchor="middle", weight="700"))
    lines.append(svg_text(WIDTH - RIGHT, TOP - 10, chart.positive_side, size=12, fill=bar_color(1, chart.positive_is_better), anchor="end", weight="700"))

    for index, (label, value) in enumerate(zip(chart.labels, chart.values)):
        y_center = TOP + row_gap * (index + 0.5)
        y = y_center - bar_height / 2
        value_x = x_for(value, min_value, max_value)
        x = min(value_x, zero_x)
        width = max(abs(value_x - zero_x), 1.0)
        fill = bar_color(value, chart.positive_is_better)
        lines.append(svg_text(LEFT - 12, y_center + 5, label, size=14, anchor="end"))
        lines.append(f'<rect x="{x:.1f}" y="{y:.1f}" width="{width:.1f}" height="{bar_height:.1f}" rx="3" fill="{fill}"/>')

        label_text = value_label(value)
        if width >= 74:
            text_x = x + 8 if value < 0 else x + width - 8
            anchor = "start" if value < 0 else "end"
            text_fill = "#ffffff"
            weight = "700"
        else:
            text_x = x - 6 if value < 0 else x + width + 6
            anchor = "end" if value < 0 else "start"
            text_fill = "#111827"
            weight = None
        lines.append(svg_text(text_x, y_center + 5, label_text, size=13, fill=text_fill, anchor=anchor, weight=weight))

    lines.append(svg_text(WIDTH / 2, HEIGHT - 44, "Signed Go-vs-C++ delta (%)", size=12, fill="#374151", anchor="middle", weight="700"))
    lines.append(svg_text(LEFT, HEIGHT - 18, chart.footnote, size=12, fill="#6b7280"))
    lines.append("</svg>")
    return "\n".join(lines) + "\n"


def first_existing(paths: Iterable[str]) -> str | None:
    for path in paths:
        if Path(path).exists():
            return path
    return None


def font(paths: Iterable[str], size: int) -> ImageFont.ImageFont:
    path = first_existing(paths)
    if path:
        try:
            return ImageFont.truetype(path, size)
        except OSError:
            pass
    return ImageFont.load_default()


def draw_text(
    draw: ImageDraw.ImageDraw,
    xy: tuple[float, float],
    text: str,
    *,
    font_obj: ImageFont.ImageFont,
    fill: str = "#111827",
    anchor: str = "la",
) -> None:
    draw.text(xy, text, fill=fill, font=font_obj, anchor=anchor)


def render_png(chart: SeriesChart, path: Path) -> None:
    min_value, max_value, ticks = symmetric_ticks(chart.values, chart.tick_step)
    zero_x = x_for(0, min_value, max_value)
    row_gap = PLOT_HEIGHT / len(chart.values)
    bar_height = min(42.0, row_gap * 0.68)

    image = Image.new("RGB", (WIDTH, HEIGHT), "#ffffff")
    draw = ImageDraw.Draw(image)
    title_font = font(FONT_BOLD_PATHS, 24)
    regular_12 = font(FONT_REGULAR_PATHS, 12)
    regular_13 = font(FONT_REGULAR_PATHS, 13)
    regular_14 = font(FONT_REGULAR_PATHS, 14)
    bold_12 = font(FONT_BOLD_PATHS, 12)
    bold_13 = font(FONT_BOLD_PATHS, 13)

    draw_text(draw, (WIDTH / 2, 34), chart.title, font_obj=title_font, anchor="mm")

    for tick in ticks:
        x = x_for(tick, min_value, max_value)
        fill = "#d1d5db" if abs(tick) < 1e-9 else "#e5e7eb"
        width = 2 if abs(tick) < 1e-9 else 1
        draw.line((x, TOP, x, HEIGHT - BOTTOM), fill=fill, width=width)
        draw_text(draw, (x, HEIGHT - 68), pct_label(tick), font_obj=regular_12, fill="#6b7280", anchor="mm")

    draw_text(draw, (LEFT, TOP - 10), chart.negative_side, font_obj=bold_12, fill=bar_color(-1, chart.positive_is_better), anchor="lm")
    draw_text(draw, (zero_x, TOP - 10), "C++ parity", font_obj=bold_12, fill="#374151", anchor="mm")
    draw_text(draw, (WIDTH - RIGHT, TOP - 10), chart.positive_side, font_obj=bold_12, fill=bar_color(1, chart.positive_is_better), anchor="rm")

    for index, (label, value) in enumerate(zip(chart.labels, chart.values)):
        y_center = TOP + row_gap * (index + 0.5)
        y = y_center - bar_height / 2
        value_x = x_for(value, min_value, max_value)
        x = min(value_x, zero_x)
        width = max(abs(value_x - zero_x), 1.0)
        fill = bar_color(value, chart.positive_is_better)
        draw_text(draw, (LEFT - 12, y_center), label, font_obj=regular_14, anchor="rm")
        draw.rounded_rectangle((x, y, x + width, y + bar_height), radius=3, fill=fill)

        label_text = value_label(value)
        if width >= 74:
            text_x = x + 8 if value < 0 else x + width - 8
            anchor = "lm" if value < 0 else "rm"
            text_fill = "#ffffff"
            label_font = bold_13
        else:
            text_x = x - 6 if value < 0 else x + width + 6
            anchor = "rm" if value < 0 else "lm"
            text_fill = "#111827"
            label_font = regular_13
        draw_text(draw, (text_x, y_center), label_text, font_obj=label_font, fill=text_fill, anchor=anchor)

    draw_text(draw, (WIDTH / 2, HEIGHT - 44), "Signed Go-vs-C++ delta (%)", font_obj=bold_12, fill="#374151", anchor="mm")
    draw_text(draw, (LEFT, HEIGHT - 18), chart.footnote, font_obj=regular_12, fill="#6b7280", anchor="lm")
    image.save(path)


def focused_charts() -> list[SeriesChart]:
    rows = read_csv(ARTIFACTS / "perf-compare-focused-summary.csv")
    labels = [f"{row['input']} L{row['level']}" for row in rows]
    size_rows = read_csv(ARTIFACTS / "focused-size-delta-signed.csv")

    return [
        SeriesChart(
            stem="focused-size-delta-signed",
            title="Focused Compressed Size Delta (Go vs C++)",
            labels=[row["label"] for row in size_rows],
            values=[float(row["size_delta_pct"]) for row in size_rows],
            tick_step=0.5,
            positive_is_better=False,
            negative_side="Go smaller",
            positive_side="Go larger",
            footnote="Signed delta = (Go size - C++ size) / C++ size. Negative bars mean Go is smaller; positive bars mean Go is larger.",
        ),
        SeriesChart(
            stem="focused-compress-throughput-delta",
            title="Focused Compression Throughput Signed Delta (Go vs C++)",
            labels=labels,
            values=[float(row["compress_speed_delta_pct"]) for row in rows],
            tick_step=10.0,
            positive_is_better=True,
            negative_side="Go slower",
            positive_side="Go faster",
            footnote="Signed delta = (Go throughput - C++ throughput) / C++ throughput. Negative bars mean Go is slower; positive bars mean Go is faster.",
        ),
        SeriesChart(
            stem="focused-compress-reuse-throughput-delta",
            title="Focused Reuse Compression Throughput Signed Delta (Go vs C++)",
            labels=labels,
            values=[float(row["compress_reuse_speed_delta_pct"]) for row in rows],
            tick_step=10.0,
            positive_is_better=True,
            negative_side="Go slower",
            positive_side="Go faster",
            footnote="Signed delta = (Go throughput - C++ throughput) / C++ throughput. Negative bars mean Go is slower; positive bars mean Go is faster.",
        ),
        SeriesChart(
            stem="focused-decompress-throughput-delta",
            title="Focused Fresh Decompression Throughput Signed Delta (Go vs C++)",
            labels=labels,
            values=[float(row["fresh_decompress_speed_delta_pct"]) for row in rows],
            tick_step=10.0,
            positive_is_better=True,
            negative_side="Go slower",
            positive_side="Go faster",
            footnote="Signed delta = (Go throughput - C++ throughput) / C++ throughput. Negative bars mean Go is slower; positive bars mean Go is faster.",
        ),
        SeriesChart(
            stem="focused-decode-reuse-throughput-delta",
            title="Focused Reuse Decode Throughput Signed Delta (Go vs C++)",
            labels=labels,
            values=[float(row["decode_reuse_speed_delta_pct"]) for row in rows],
            tick_step=10.0,
            positive_is_better=True,
            negative_side="Go slower",
            positive_side="Go faster",
            footnote="Signed delta = (Go throughput - C++ throughput) / C++ throughput. Negative bars mean Go is slower; positive bars mean Go is faster.",
        ),
    ]


def main() -> None:
    CHARTS.mkdir(parents=True, exist_ok=True)
    for chart in focused_charts():
        (CHARTS / f"{chart.stem}.svg").write_text(render_svg(chart))
        render_png(chart, CHARTS / f"{chart.stem}.png")


if __name__ == "__main__":
    main()
