#!/usr/bin/env python3
"""PWA 아이콘 생성기. 저장소 루트에서 실행한다: python3 scripts/gen-icons.py (Pillow 필요)"""
from PIL import Image, ImageDraw, ImageFont

FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
ACCENT = (31, 111, 92, 255)   # --accent 라이트 모드
FG = (251, 250, 248, 255)     # --bg 라이트 모드
TEXT = "TIL"
SS = 4  # 슈퍼샘플링 배율


def draw_icon(size, radius_ratio, text_ratio, full_bleed):
    s = size * SS
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    if full_bleed:
        d.rectangle([0, 0, s, s], fill=ACCENT)
    else:
        d.rounded_rectangle([0, 0, s - 1, s - 1], radius=int(s * radius_ratio), fill=ACCENT)

    # 목표 너비에 맞는 폰트 크기를 이분 탐색 없이 한 번의 비율 보정으로 찾는다.
    target = s * text_ratio
    probe = ImageFont.truetype(FONT, 100)
    w = d.textlength(TEXT, font=probe, features=["-kern"])
    font = ImageFont.truetype(FONT, max(1, int(100 * target / w)))

    # 자간을 조금 벌려 브랜드(.brand letter-spacing: 0.08em)와 느낌을 맞춘다.
    tracking = int(font.size * 0.08)
    widths = [d.textlength(c, font=font) for c in TEXT]
    total = sum(widths) + tracking * (len(TEXT) - 1)
    box = d.textbbox((0, 0), TEXT, font=font)
    x = (s - total) / 2
    y = (s - (box[3] - box[1])) / 2 - box[1]
    for c, cw in zip(TEXT, widths):
        d.text((x, y), c, font=font, fill=FG)
        x += cw + tracking

    return img.resize((size, size), Image.LANCZOS)


def save(img, path, keep_alpha=True):
    if not keep_alpha:
        img = img.convert("RGB")
    img.save(path, "PNG", optimize=True)
    print(path, img.size, img.mode)


save(draw_icon(192, 0.22, 0.56, False), "static/icon-192.png")
save(draw_icon(512, 0.22, 0.56, False), "static/icon-512.png")
# maskable: 배경은 캔버스를 꽉 채우고 글자는 안쪽 80% 안전 영역에 둔다.
save(draw_icon(512, 0, 0.44, True), "static/icon-maskable-512.png")
# iOS 는 스스로 모서리를 깎으므로 투명도 없이 꽉 채운다.
save(draw_icon(180, 0, 0.52, True), "static/apple-touch-icon.png", keep_alpha=False)
