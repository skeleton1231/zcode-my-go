// zgo-fs.js — Zgo 浏览器目标的内存文件系统。
//
// Go 的 js/wasm syscall 层（go/src/syscall/fs_js.go）需要一个 Node 形态的
// fs 对象，且 O_* 常量取真实值；wasm_exec.js 的回退实现只有 stdout 的
// writeSync，常量全为 -1，任何 open/mkdir 都报 "not implemented on js"。
// 本脚本定义完整的内存 FS，必须排在 wasm_exec.js 之前加载——它的
// `if (!globalThis.fs)` 会保留页面上已有的实现。
//
// 数据只活在页面内存：刷新即清空（localStorage/IndexedDB 持久化桥接是
// wasm 路线的后续部分）。由 cmd/webfs 在 mygo build -platform web 后注入。
(() => {
	"use strict";
	if (globalThis.fs && globalThis.fs.__zgo) return; // 幂等

	const S_IFREG = 0x8000; // 0100000
	const S_IFDIR = 0x4000; // 0040000
	const nowMs = () => Date.now();

	// 节点：目录持 entries Map，文件持 Uint8Array。
	const mkDir = () => ({ dir: true, entries: new Map(), mtime: nowMs(), ctime: nowMs() });
	const mkFile = () => ({ dir: false, data: new Uint8Array(0), mtime: nowMs(), ctime: nowMs() });
	const root = mkDir();

	const parts = p => String(p).split("/").filter(s => s !== "" && s !== ".");
	const lookup = ps => {
		let n = root;
		for (const seg of ps) {
			if (!n.dir) return null;
			n = n.entries.get(seg);
			if (!n) return null;
		}
		return n;
	};
	const parent = ps => (ps.length === 0 ? null : lookup(ps.slice(0, -1)));
	const name = ps => ps[ps.length - 1];
	const fail = (code, p) => {
		const e = new Error(code + ": " + p);
		e.code = code;
		return e;
	};
	const stats = n => ({
		dev: 0, ino: 0, nlink: 1, uid: 0, gid: 0, rdev: 0,
		blksize: 4096, blocks: Math.ceil((n.dir ? 0 : n.data.length) / 512),
		mode: (n.dir ? S_IFDIR : S_IFREG) | 0o666,
		size: n.dir ? 4096 : n.data.length,
		atimeMs: n.mtime, mtimeMs: n.mtime, ctimeMs: n.ctime,
		isDirectory: () => n.dir,
	});

	// fd 表：0-2 被 Go 层占用，从 100 起分配。
	const fds = new Map();
	let nextFd = 100;
	const reserve = entry => {
		const fd = nextFd++;
		fds.set(fd, entry);
		return fd;
	};

	// stdout/stderr：wasm_exec 的回退不再安装，panic 与 println 落在这里。
	const outBuf = { 1: "", 2: "" };
	const writeSync = (fd, buf) => {
		const chunk = new TextDecoder().decode(buf);
		outBuf[fd] = (outBuf[fd] || "") + chunk;
		const nl = outBuf[fd].lastIndexOf("\n");
		if (nl !== -1) {
			(fd === 2 ? console.error : console.log)(outBuf[fd].slice(0, nl));
			outBuf[fd] = outBuf[fd].slice(nl + 1);
		}
		return buf.length;
	};

	const mkdirp = ps => {
		let n = root;
		for (const seg of ps) {
			let next = n.entries.get(seg);
			if (!next) {
				next = mkDir();
				n.entries.set(seg, next);
			} else if (!next.dir) {
				return fail("ENOTDIR", seg);
			}
			n = next;
		}
		return null;
	};
	mkdirp(["tmp"]); // os.TempDir 的默认根

	const O = { O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2, O_CREAT: 64, O_EXCL: 128, O_TRUNC: 512, O_APPEND: 1024, O_DIRECTORY: 65536 };

	globalThis.fs = {
		__zgo: true,
		constants: O,
		writeSync,
		write(fd, buf, offset, length, position, cb) {
			if (fd === 1 || fd === 2) {
				cb(null, writeSync(fd, buf.subarray(offset, offset + length)));
				return;
			}
			const e = fds.get(fd);
			if (!e) { cb(fail("EBADF", fd)); return; }
			if (e.node.dir) { cb(fail("EISDIR", e.path)); return; }
			let pos = position === null || position === undefined ? e.pos : position;
			const end = pos + length;
			if (end > e.node.data.length) {
				const grown = new Uint8Array(end);
				grown.set(e.node.data);
				e.node.data = grown;
			}
			e.node.data.set(buf.subarray(offset, offset + length), pos);
			e.node.mtime = nowMs();
			e.pos = pos + length;
			cb(null, length);
		},
		read(fd, buf, offset, length, position, cb) {
			const e = fds.get(fd);
			if (!e) { cb(fail("EBADF", fd)); return; }
			if (e.node.dir) { cb(fail("EISDIR", e.path)); return; }
			let pos = position === null || position === undefined ? e.pos : position;
			const slice = e.node.data.subarray(Math.min(pos, e.node.data.length), Math.min(pos + length, e.node.data.length));
			buf.set(slice, offset);
			e.pos = pos + slice.length;
			cb(null, slice.length);
		},
		open(path, flags, mode, cb) {
			const ps = parts(path);
			if (flags & O.O_DIRECTORY) {
				const n = lookup(ps);
				if (!n || !n.dir) { cb(fail("ENOENT", path)); return; }
				cb(null, reserve({ node: n, pos: 0, path }));
				return;
			}
			let n = lookup(ps);
			const p = parent(ps);
			if (!n) {
				if (!(flags & O.O_CREAT) || !p || !p.dir) { cb(fail("ENOENT", path)); return; }
				n = mkFile();
				p.entries.set(name(ps), n);
			} else {
				if (n.dir) { cb(fail("EISDIR", path)); return; }
				if (flags & O.O_EXCL) { cb(fail("EEXIST", path)); return; }
				if (flags & O.O_TRUNC) { n.data = new Uint8Array(0); n.mtime = nowMs(); }
			}
			cb(null, reserve({ node: n, pos: (flags & O.O_APPEND) ? n.data.length : 0, path }));
		},
		close(fd, cb) { fds.delete(fd); cb(null, 0); },
		stat(path, cb) {
			const n = lookup(parts(path));
			n ? cb(null, stats(n)) : cb(fail("ENOENT", path));
		},
		lstat(path, cb) { this.stat(path, cb); },
		fstat(fd, cb) {
			const e = fds.get(fd);
			e ? cb(null, stats(e.node)) : cb(fail("EBADF", fd));
		},
		mkdir(path, perm, cb) {
			const ps = parts(path);
			const p = parent(ps);
			if (!p || !p.dir) { cb(fail("ENOENT", path)); return; }
			if (p.entries.has(name(ps))) { cb(fail("EEXIST", path)); return; }
			p.entries.set(name(ps), mkDir());
			cb(null, undefined);
		},
		readdir(path, cb) {
			const n = lookup(parts(path));
			if (!n || !n.dir) { cb(fail("ENOENT", path)); return; }
			cb(null, [...n.entries.keys()]);
		},
		unlink(path, cb) {
			const ps = parts(path);
			const n = lookup(ps);
			const p = parent(ps);
			if (!n || !p) { cb(fail("ENOENT", path)); return; }
			if (n.dir) { cb(fail("EISDIR", path)); return; }
			p.entries.delete(name(ps));
			cb(null, undefined);
		},
		rmdir(path, cb) {
			const ps = parts(path);
			const n = lookup(ps);
			const p = parent(ps);
			if (!n || !p) { cb(fail("ENOENT", path)); return; }
			if (!n.dir) { cb(fail("ENOTDIR", path)); return; }
			if (n.entries.size !== 0) { cb(fail("ENOTEMPTY", path)); return; }
			p.entries.delete(name(ps));
			cb(null, undefined);
		},
		rename(from, to, cb) {
			const fps = parts(from), tps = parts(to);
			const n = lookup(fps), fp = parent(fps), tp = parent(tps);
			if (!n || !fp || !tp || !tp.dir) { cb(fail("ENOENT", from)); return; }
			tp.entries.set(name(tps), n); // POSIX 覆盖语义
			fp.entries.delete(name(fps));
			cb(null, undefined);
		},
		fsync(fd, cb) { cb(null, undefined); },
		ftruncate(fd, size, cb) {
			const e = fds.get(fd);
			if (!e) { cb(fail("EBADF", fd)); return; }
			const data = new Uint8Array(size);
			data.set(e.node.data.subarray(0, size));
			e.node.data = data;
			cb(null, undefined);
		},
		chmod(path, mode, cb) { cb(null, undefined); },
		fchmod(fd, mode, cb) { cb(null, undefined); },
		chown(path, uid, gid, cb) { cb(null, undefined); },
		fchown(fd, uid, gid, cb) { cb(null, undefined); },
		lchown(path, uid, gid, cb) { cb(null, undefined); },
		utimes(path, atime, mtime, cb) { cb(null, undefined); },
	};
})();
