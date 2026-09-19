package geoip

import "strings"

// chinaProvince recognizes provider subdivision names without guessing from a
// city name. Unknown subdivisions remain empty rather than inventing a province.
func chinaProvince(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	for _, suffix := range []string{" zhuang autonomous region", " hui autonomous region", " uygur autonomous region", " uyghur autonomous region", " autonomous region", " municipality", " province", " sheng", " shi", "自治区", "省", "市"} {
		key = strings.TrimSuffix(key, suffix)
	}
	return chinaProvinces[strings.TrimSpace(key)]
}

var chinaProvinces = func() map[string]string {
	names := map[string]string{
		"anhui": "安徽", "beijing": "北京", "chongqing": "重庆", "fujian": "福建",
		"gansu": "甘肃", "guangdong": "广东", "guangxi": "广西", "guizhou": "贵州",
		"hainan": "海南", "hebei": "河北", "heilongjiang": "黑龙江", "henan": "河南",
		"hubei": "湖北", "hunan": "湖南", "jiangsu": "江苏", "jiangxi": "江西",
		"jilin": "吉林", "liaoning": "辽宁", "inner mongolia": "内蒙古", "nei mongol": "内蒙古",
		"neimenggu": "内蒙古", "ningxia": "宁夏", "qinghai": "青海", "shaanxi": "陕西",
		"shandong": "山东", "shanghai": "上海", "shanxi": "山西", "sichuan": "四川",
		"tianjin": "天津", "tibet": "西藏", "xizang": "西藏", "xinjiang": "新疆",
		"yunnan": "云南", "zhejiang": "浙江", "hong kong": "香港", "macao": "澳门",
		"macau": "澳门", "taiwan": "台湾",
		"广西壮族": "广西", "宁夏回族": "宁夏", "新疆维吾尔": "新疆",
	}
	for _, name := range names {
		names[name] = name
	}
	return names
}()
