"""Compose only crops of real matched screenshots, with captions outside pixels."""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont
import json
root=Path(__file__).resolve().parents[1]/'evidence/dropdowns'
font='/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc'
title=ImageFont.truetype(font,25);label=ImageFont.truetype(font,19);body=ImageFont.truetype(font,16)
rows=[
 ('桌面 · 深色 / 证书来源','desktop-dark-certificate-filters',(440,120,830,430)),
 ('桌面 · 浅色 / 流量统计周期','desktop-light-traffic-cycle',(710,430,1100,760)),
 ('手机 · 浅色 / DNS 服务商','mobile-light-certificate-provider',(0,240,390,720)),
 ('手机 · 深色 / TLS 证书方式','mobile-dark-tls',(0,410,390,844)),
]
width=848;header=118;footer=58;gap=42;height=header+sum(c[3]-c[1]+gap for _,_,c in rows)+footer
im=Image.new('RGB',(width,height),'#eef0f4');d=ImageDraw.Draw(im)
d.text((24,12),'全站下拉框 · 真实截图修改前 / 修改后',font=title,fill='#1a2435')
d.text((24,49),'相同账号、数据、主题、视口与页面状态；原图局部裁剪，无重绘',font=body,fill='#49556b')
d.text((24,83),'修改前（上轮交付版本）',font=label,fill='#1a2435');d.text((434,83),'修改后（本轮整改）',font=label,fill='#1a2435')
y=header
for name,key,crop in rows:
 d.text((24,y+7),name,font=label,fill='#1a2435')
 for column,stage in enumerate(['before','after']):
  raw=Image.open(root/f'{stage}-{key}.png').convert('RGB');pic=raw.crop(crop);assert pic.width==390;im.paste(pic,(24+410*column,y+gap))
 y+=crop[3]-crop[1]+gap
d.text((24,y+12),'全部页面、桌面 / 手机 / 平板、浅深主题的完整原图见 dropdowns.md。',font=body,fill='#49556b')
im.save(root/'before-after.png');(root/'comparison-crops.json').write_text(json.dumps([{'label':n,'key':k,'crop':c} for n,k,c in rows],ensure_ascii=False,indent=2)+'\n');print(im.size)
