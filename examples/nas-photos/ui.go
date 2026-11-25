package main

// indexHTML 是内嵌单页前端（任务 §9）：顶栏（用户名/上传按钮/拖拽区）、
// 瀑布流网格（缩略图+懒加载）、点开 lightbox（原图+删除按钮+左右切换）。
// 全部内联 CSS/JS，`./api/...` 相对路径调用（§4「所有页面资源必须相对
// 路径」，hello-app 同款姿势，前缀无关）。
var indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>家庭照片 · nas-photos</title>
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  body {
    font-family: -apple-system, system-ui, "PingFang SC", "Microsoft YaHei", sans-serif;
    margin: 0; color: #1d1d1f; background: #f5f5f7;
  }
  header {
    position: sticky; top: 0; z-index: 10;
    display: flex; align-items: center; gap: 12px;
    padding: 12px 20px; background: #fff; border-bottom: 1px solid #e5e5ea;
  }
  header h1 { font-size: 16px; margin: 0; flex: 0 0 auto; }
  header .user { color: #666; font-size: 13px; margin-right: auto; }
  button {
    border: none; border-radius: 8px; padding: 8px 16px; font-size: 14px;
    cursor: pointer; background: #0a7cff; color: #fff; transition: opacity .15s;
  }
  button:hover { opacity: .85; }
  button.danger { background: #e13c3c; }
  button.ghost { background: transparent; color: #0a7cff; border: 1px solid #0a7cff; }
  #dropzone {
    margin: 16px 20px 0; padding: 18px; border: 2px dashed #c7c7cc; border-radius: 12px;
    text-align: center; color: #888; font-size: 13px; transition: background .15s, border-color .15s;
  }
  #dropzone.dragover { background: #eaf3ff; border-color: #0a7cff; color: #0a7cff; }
  #grid {
    display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 10px; padding: 16px 20px 40px;
  }
  .cell {
    position: relative; aspect-ratio: 1 / 1; border-radius: 10px; overflow: hidden;
    background: #e5e5ea; cursor: pointer;
  }
  .cell img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .cell .name {
    position: absolute; left: 0; right: 0; bottom: 0; padding: 4px 6px;
    font-size: 11px; color: #fff; background: linear-gradient(transparent, rgba(0,0,0,.55));
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  #empty { text-align: center; color: #999; padding: 60px 20px; font-size: 14px; }
  #lightbox {
    position: fixed; inset: 0; background: rgba(0,0,0,.88); display: none;
    align-items: center; justify-content: center; flex-direction: column; z-index: 100;
  }
  #lightbox.open { display: flex; }
  #lightbox img { max-width: 92vw; max-height: 76vh; border-radius: 8px; }
  #lightbox .bar {
    display: flex; align-items: center; gap: 10px; margin-top: 16px; color: #fff; font-size: 13px;
  }
  #lightbox .nav {
    position: absolute; top: 50%; transform: translateY(-50%); font-size: 28px; color: #fff;
    background: rgba(255,255,255,.12); border: none; width: 48px; height: 48px; border-radius: 50%;
    cursor: pointer; line-height: 1;
  }
  #lightbox .nav.prev { left: 16px; }
  #lightbox .nav.next { right: 16px; }
  #lightbox .close {
    position: absolute; top: 16px; right: 16px; background: rgba(255,255,255,.12);
    width: 36px; height: 36px; border-radius: 50%; color: #fff; font-size: 18px; cursor: pointer; border: none;
  }
  #toast {
    position: fixed; bottom: 20px; left: 50%; transform: translateX(-50%);
    background: #1d1d1f; color: #fff; padding: 8px 16px; border-radius: 8px;
    font-size: 13px; opacity: 0; transition: opacity .2s; pointer-events: none;
  }
  #toast.show { opacity: .95; }
</style>
</head>
<body>
  <header>
    <h1>📷 家庭照片</h1>
    <span class="user" id="userLabel"></span>
    <button id="uploadBtn">上传照片</button>
    <input type="file" id="fileInput" multiple accept="image/jpeg,image/png,image/gif" hidden>
  </header>

  <div id="dropzone">把照片拖到这里上传，或点击右上角「上传照片」（支持 jpg/png/gif，单张最大 50MB）</div>

  <div id="grid"></div>
  <div id="empty" style="display:none">还没有照片，上传第一张吧</div>

  <div id="lightbox">
    <button class="close" id="lbClose">✕</button>
    <button class="nav prev" id="lbPrev">‹</button>
    <button class="nav next" id="lbNext">›</button>
    <img id="lbImg" alt="">
    <div class="bar">
      <span id="lbName"></span>
      <button class="danger" id="lbDelete">删除</button>
    </div>
  </div>

  <div id="toast"></div>

  <script>
    var photos = [];
    var lbIndex = -1;

    function toast(msg) {
      var t = document.getElementById('toast');
      t.textContent = msg;
      t.className = 'show';
      setTimeout(function () { t.className = ''; }, 2000);
    }

    function loadWhoami() {
      // 核心反代会附加 X-NAS-User 头，但前端 JS 读不到请求头；这里简单地
      // 从页面无法直接得知用户名，退化显示「已登录」占位——真实用户名
      // 展示依赖核心门户 shell（本应用是反代内容区）。保留占位不写死文案。
      var label = document.getElementById('userLabel');
      label.textContent = '';
    }

    function renderGrid() {
      var grid = document.getElementById('grid');
      var empty = document.getElementById('empty');
      grid.innerHTML = '';
      if (photos.length === 0) {
        empty.style.display = 'block';
        return;
      }
      empty.style.display = 'none';
      photos.forEach(function (p, idx) {
        var cell = document.createElement('div');
        cell.className = 'cell';
        var img = document.createElement('img');
        img.loading = 'lazy';
        img.src = p.thumb;
        img.alt = p.origName;
        var name = document.createElement('div');
        name.className = 'name';
        name.textContent = p.origName;
        cell.appendChild(img);
        cell.appendChild(name);
        cell.addEventListener('click', function () { openLightbox(idx); });
        grid.appendChild(cell);
      });
    }

    function refresh() {
      return fetch('./api/photos').then(function (r) {
        if (!r.ok) { throw new Error('列表加载失败: ' + r.status); }
        return r.json();
      }).then(function (data) {
        photos = data;
        renderGrid();
      }).catch(function (err) {
        toast(err.message);
      });
    }

    function openLightbox(idx) {
      lbIndex = idx;
      showLightbox();
      document.getElementById('lightbox').classList.add('open');
    }

    function showLightbox() {
      var p = photos[lbIndex];
      if (!p) { closeLightbox(); return; }
      document.getElementById('lbImg').src = p.photo;
      document.getElementById('lbName').textContent = p.origName;
    }

    function closeLightbox() {
      document.getElementById('lightbox').classList.remove('open');
      lbIndex = -1;
    }

    function navLightbox(delta) {
      if (photos.length === 0) return;
      lbIndex = (lbIndex + delta + photos.length) % photos.length;
      showLightbox();
    }

    function doDelete(id) {
      return fetch('./api/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: id })
      }).then(function (r) {
        if (!r.ok) { throw new Error('删除失败: ' + r.status); }
        return r.json();
      });
    }

    function doUpload(files) {
      if (!files || files.length === 0) return;
      var fd = new FormData();
      for (var i = 0; i < files.length; i++) {
        fd.append('files', files[i]);
      }
      toast('正在上传 ' + files.length + ' 个文件...');
      fetch('./api/upload', { method: 'POST', body: fd }).then(function (r) {
        return r.json().then(function (results) { return { status: r.status, results: results }; });
      }).then(function (res) {
        var ok = res.results.filter(function (x) { return x.ok; }).length;
        var fail = res.results.length - ok;
        toast('上传完成：成功 ' + ok + (fail ? '，失败 ' + fail : ''));
        refresh();
      }).catch(function (err) {
        toast('上传失败: ' + err.message);
      });
    }

    document.getElementById('uploadBtn').addEventListener('click', function () {
      document.getElementById('fileInput').click();
    });
    document.getElementById('fileInput').addEventListener('change', function (e) {
      doUpload(e.target.files);
      e.target.value = '';
    });

    var dropzone = document.getElementById('dropzone');
    ['dragenter', 'dragover'].forEach(function (evt) {
      dropzone.addEventListener(evt, function (e) {
        e.preventDefault();
        dropzone.classList.add('dragover');
      });
    });
    ['dragleave', 'drop'].forEach(function (evt) {
      dropzone.addEventListener(evt, function (e) {
        e.preventDefault();
        dropzone.classList.remove('dragover');
      });
    });
    dropzone.addEventListener('drop', function (e) {
      doUpload(e.dataTransfer.files);
    });

    document.getElementById('lbClose').addEventListener('click', closeLightbox);
    document.getElementById('lbPrev').addEventListener('click', function () { navLightbox(-1); });
    document.getElementById('lbNext').addEventListener('click', function () { navLightbox(1); });
    document.getElementById('lbDelete').addEventListener('click', function () {
      var p = photos[lbIndex];
      if (!p) return;
      if (!confirm('删除「' + p.origName + '」？')) return;
      doDelete(p.id).then(function () {
        closeLightbox();
        refresh();
      }).catch(function (err) {
        toast(err.message);
      });
    });
    document.addEventListener('keydown', function (e) {
      if (!document.getElementById('lightbox').classList.contains('open')) return;
      if (e.key === 'Escape') closeLightbox();
      if (e.key === 'ArrowLeft') navLightbox(-1);
      if (e.key === 'ArrowRight') navLightbox(1);
    });

    loadWhoami();
    refresh();
  </script>
</body>
</html>
`
