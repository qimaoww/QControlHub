"""Join real, unscaled screenshots; captions remain outside product pixels."""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

root = Path(__file__).resolve().parents[1] / 'evidence/round3'
font_path = '/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc'
title = ImageFont.truetype(font_path, 27)
label = ImageFont.truetype(font_path, 20)
body = ImageFont.truetype(font_path, 17)
rows = [
    ('01 证书长标题与关闭按钮', 'mobile-dark-long-title'),
    ('02 Caddy 长域名与卡片边界', 'mobile-light-long-source'),
    ('03 实例绑定的长配置名', 'mobile-light-long-instance'),
    ('04 DNS 验证记录与 TXT 内容', 'mobile-light-long-dns'),
]
padding, gap, width, row_height, header, footer = 24, 20, 390, 902, 132, 62
output = Image.new('RGB', (2 * width + 2 * padding + gap, header + len(rows) * row_height + footer), '#eef0f4')
draw = ImageDraw.Draw(output)
draw.text((padding, 15), '本轮继续整改 · 真实截图前后对照', font=title, fill='#1a2435')
draw.text((padding, 55), '相同账号、数据、主题和视口（390 × 844）', font=body, fill='#49556b')
draw.text((padding, 92), '修改前（上轮交付版本）', font=label, fill='#1a2435')
draw.text((padding + width + gap, 92), '修改后（当前工作树）', font=label, fill='#1a2435')
for index, (name, filename) in enumerate(rows):
    y = header + index * row_height
    draw.text((padding, y + 8), name, font=label, fill='#1a2435')
    for column, stage in enumerate(['before', 'after']):
        screenshot = Image.open(root / f'{stage}-{filename}.png').convert('RGB')
        assert screenshot.size == (390, 844)
        output.paste(screenshot, (padding + column * (width + gap), y + 42))
draw.text((padding, header + len(rows) * row_height + 8), '截图未经重绘；仅拼接并在画面外添加标题。', font=body, fill='#49556b')
output.save(root / 'before-after.png')
