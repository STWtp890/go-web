Optimized tool selection我来阅读 frontello 文件夹中的关键文件。

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/README.txt#1-1), lines 1 to 50

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/config.json#1-1), lines 1 to 100

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/demo.html#1-1), lines 1 to 100

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/css/fontello.css#1-1), lines 1 to 100

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/css/fontello-codes.css#1-1), lines 1 to 100

让我继续读取剩余的图标列表。

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/css/fontello.css#100-100), lines 100 to 200

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/font)

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/css/fontello.css#200-200), lines 200 to 280

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/vue-dashboard/src/assets/frontello/css/fontello.css#280-280), lines 280 to 365

以下是 `frontello` 文件夹的完整分析：

---

## 📦 Frontello 字体图标库概览

**来源**：由 [fontello.com](https://fontello.com) 生成的开源 Web 字体图标包。

---

### 📁 文件结构

```
frontello/
├── config.json          # Fontello 配置文件（可重新导入 fontello.com 编辑）
├── demo.html            # 图标展示 Demo 页面
├── LICENSE.txt          # 字体许可信息
├── README.txt           # 使用说明
├── css/
│   ├── fontello.css           # 主 CSS（@font-face + 全部图标码）
│   ├── fontello-codes.css     # 仅图标码（无 @font-face，适合自定义加载）
│   ├── fontello-ie7.css       # IE7 兼容版
│   ├── fontello-ie7-codes.css # IE7 兼容版（仅图标码）
│   ├── fontello-embedded.css  # Base64 内嵌字体版
│   └── animation.css          # 图标动画效果
└── font/
    ├── fontello.eot
    ├── fontello.svg
    ├── fontello.ttf
    ├── fontello.woff
    └── fontello.woff2
```

---

### 🎨 图标集来源：**Typicons**

所有图标均来自 Typicons 字体集，总计约 **130+ 个图标**。

---

### 🔤 CSS 前缀与用法

- CSS 前缀：`icon-`
- 使用方式：`<i class="icon-xxxx"></i>`

```html
<!-- 示例 -->
<i class="icon-heart"></i>        <!-- 空心爱心 -->
<i class="icon-heart-filled"></i> <!-- 实心爱心 -->
<i class="icon-search"></i>       <!-- 搜索 -->
<i class="icon-user"></i>         <!-- 用户 -->
```

---

### 📋 图标完整列表（按类别）

| 类别 | 图标 |
|------|------|
| **用户** | `user`, `user-outline`, `users`, `users-outline`, `user-add`, `user-add-outline`, `user-delete`, `user-delete-outline` |
| **媒体** | `music`, `music-outline`, `video`, `videocam`, `videocam-outline`, `picture`, `picture-outline`, `camera`, `camera-outline`, `mic`, `mic-outline` |
| **通信** | `mail`, `bell`, `chat`, `chat-alt`, `comment`, `phone`, `phone-outline`, `contacts`, `vcard`, `address` |
| **导航** | `home`, `home-outline`, `compass`, `map`, `location`, `location-outline`, `direction`, `direction-outline`, `target`, `target-outline`, `globe`, `globe-outline`, `globe-alt`, `globe-alt-outline` |
| **操作** | `search`, `search-outline`, `heart`, `heart-filled`, `star`, `star-filled`, `thumbs-up`, `thumbs-down`, `bookmark`, `flag`, `flag-filled` |
| **状态** | `ok`, `ok-outline`, `cancel`, `cancel-outline`, `cancel-alt`, `cancel-alt-filled`, `cancel-circled`, `cancel-circled-outline`, `plus`, `plus-outline`, `minus`, `minus-outline`, `info`, `info-outline`, `attention`, `attention-filled`, `warning`, `warning-empty`, `block`, `block-outline` |
| **安全** | `lock`, `lock-filled`, `lock-open`, `lock-open-filled`, `key`, `key-outline`, `eye`, `eye-outline`, `pin`, `pin-outline`, `certificate`, `certificate-outline` |
| **文件** | doc, `doc-text`, `doc-add`, `doc-remove`, `folder`, `folder-add`, `folder-delete`, `archive`, `box`, `news`, `clipboard` |
| **编辑** | `pencil`, `pen`, `feather`, `edit`, `brush`, `scissors`, `scissors-outline`, `pipette` |
| **布局** | `th`, `th-outline`, `th-large`, `th-large-outline`, `th-list`, `th-list-outline`, `menu`, `menu-outline`, `popup`, `popup-1` |
| **箭头** | `left-open`, `left-open-outline`, `right-open`, `right-open-outline`, `left`, `right`, `up`, `down`, `left-outline`, `right-outline`, `up-outline`, `down-outline`, `left-small`, `right-small`, `up-small`, `down-small` |
| **播放** | `play`, `play-outline`, `stop`, `stop-outline`, `pause`, `pause-outline`, `record`, `record-outline`, `eject`, `eject-outline`, `eject-alt`, `eject-alt-outline`, `fast-fw`, `fast-fw-outline`, `rewind`, `rewind-outline` |
| **传输** | `download`, `download-outline`, `upload`, `upload-outline`, `upload-cloud`, `upload-cloud-outline`, `export`, `export-outline`, `reply`, `reply-outline`, `forward`, `forward-outline` |
| **循环** | `cw`, `cw-outline`, `arrows-cw`, `arrows-cw-outline`, `loop`, `loop-outline`, `loop-alt`, `loop-alt-outline`, `shuffle` |
| **缩放** | `zoom-in`, `zoom-in-outline`, `zoom-out`, `zoom-out-outline`, `resize-full`, `resize-full-outline`, `resize-normal`, `resize-normal-outline`, `move`, `move-outline` |
| **设备** | `desktop`, `laptop`, `tablet`, `mobile`, `wifi`, `wifi-outline`, `plug`, `bat1`~`bat4`, `bat-charge` |
| **工具** | `cog`, `cog-outline`, `wrench`, `wrench-outline`, `trash`, `basket`, `lightbulb`, `gift`, `clock`, `wristwatch`, `stopwatch` |
| **数据** | `chart`, `chart-outline`, `chart-alt`, `chart-alt-outline`, `chart-bar`, `chart-bar-outline`, `chart-pie`, `chart-pie-outline`, `chart-line`, `database`, `tag`, `tags` |
| **天气** | `sun`, `sun-filled`, `moon`, `cloud`, `cloud-sun`, `cloud-flash`, `cloud-wind`, `rain`, `drizzle`, `snow`, `wind`, `waves`, `waves-outline`, `flash`, `flash-outline` |
| **数学** | `divide`, `divide-outline`, `eq`, `eq-outline`, `infinity`, `infinity-outline`, `pi`, `pi-outline`, `calculator` |
| **杂项** | `link`, `link-outline`, `attach`, `attach-outline`, `code`, `code-outline`, `print`, `rss`, `rss-outline`, `lifebuoy`, `briefcase`, `leaf`, `tree`, `anchor`, `anchor-outline`, `puzzle`, `puzzle-outline`, `flask`, `wine`, `coffee`, `beer`, `ticket`, `credit-card`, `flow-split`, `flow-merge`, `flow-parallel`, `flow-cross`, `looped-square-outline`, `looped-square-interest`, `sort-alphabet`, `sort-alphabet-outline`, `sort-numeric`, `sort-numeric-outline`, `contrast`, `power`, `power-outline`, `check`, `check-outline`, `at`, `at-circled`, `back`, `volume-off`, `volume-low`, `volume-middle`, `volume-high`, `headphones` |
| **社交** | `dribbble`, `dribbble-circled`, `facebook`, `facebook-circled`, `flickr`, `flickr-circled`, `github`, `github-circled`, `lastfm`, `lastfm-circled`, `linkedin`, `linkedin-circled`, `pinterest`, `pinterest-circled`, `skype`, `skype-outline`, `tumbler`, `tumbler-circled`, `twitter`, `twitter-circled`, `vimeo`, `vimeo-circled` |

---

### ⚙️ 字体格式支持

- **EOT** — IE 兼容
- **WOFF2** — 现代浏览器（压缩率最高）
- **WOFF** — 主流浏览器
- **TTF** — 通用 TrueType
- **SVG** — iOS Safari 旧版支持

---

### 💡 关键要点

1. 图标分为 **outline（线性）** 和 **filled（实心）** 两种风格
2. config.json 可重新上传到 [fontello.com](https://fontello.com) 继续编辑、增减图标
3. 大部分图标使用 `icon` CSS 类前缀
4. 项目已在 demo.html 中提供了可视化展示页面

这个库覆盖了 UI 开发中常用的大部分图标需求，非常适合后台管理系统的图标场景。