// 把 src/ 下的文件打包成一个可以直接双击打开的 index.html（无外部依赖）。
// 用法：node prototype/build.js
'use strict';
const fs = require('fs');
const path = require('path');

const src = path.join(__dirname, 'src');
const read = (f) => fs.readFileSync(path.join(src, f), 'utf8');
const scripts = ['crypto.js', 'catalog.js', 'engine.js', 'boost.js', 'seed.js', 'ui-core.js', 'ui-form.js', 'ui-buyer.js', 'ui-admin-games.js', 'ui-ops.js'];

// 防止脚本里的 "</script" 提前结束标签
const inline = (code) => code.replace(/<\/script/gi, '<\\/script');

const html = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>独角数卡 · 自动代练（本地原型）</title>
<style>
${read('app.css')}
</style>
</head>
<body>
<div id="app"><p style="padding:40px">正在加载…</p></div>
${scripts.map((f) => `<script>/* ${f} */\n${inline(read(f))}\n</script>`).join('\n')}
</body>
</html>
`;

const out = path.join(__dirname, 'index.html');
fs.writeFileSync(out, html);
console.log(`已生成 ${out}（${(html.length / 1024).toFixed(1)} KB）`);
