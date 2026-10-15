# Z1 4-Bay NAS prototype enclosure — parametric model (FreeCAD 1.0, headless)
# Run: freecadcmd build_model.py
# Units: mm. Origin: front-bottom-left of the machine. X=width, Y=depth, Z=height.
import os, FreeCAD, Part, Mesh, Import
from FreeCAD import Vector as V

OUT = os.path.dirname(os.path.abspath(__file__))
STL = os.path.join(OUT, "stl")
os.makedirs(STL, exist_ok=True)

# ---------------- parameters ----------------
EX_W, EX_D, EX_H = 186.0, 210.0, 184.0   # outer envelope (incl. wood base)
BASE_H   = 8.0        # walnut base / printed base plate
WALL     = 3.0        # shell wall (FDM prototype)
FLOOR_T  = 3.0        # shell floor thickness
SHELL_Z0 = BASE_H                     # shell sits on base, z=8..184
DECK_Z   = 49.0       # cage floor top
BAY_Z0   = 52.5       # bottom bay opening floor
BAY_PITCH= 33.0
BAY_H    = 29.5       # tray opening height
BAY_W    = 122.0      # tray opening width
BAY_X0   = 14.0       # bay zone left edge
CAGE_X1  = BAY_X0 + BAY_W + 2*2.0 + 6.0   # guide wall outer right =144? compute below
N_BAYS   = 4
BP_Y     = 168.0      # backplane bracket front face
FAN_R    = 46.0
FAN_C    = (93.0, 115.0)   # rear fan center (x,z)
TOP_Z1   = 181.0      # top wall underside

doc = FreeCAD.newDocument("z1-4bay-prototype")

def box(x,y,z, dx,dy,dz):
    return Part.makeBox(dx,dy,dz, V(x,y,z))
def cyl(cx,cy,cz, r,h, axis='z'):
    d = {'x':V(1,0,0),'y':V(0,1,0),'z':V(0,0,1)}[axis]
    return Part.makeCylinder(r,h, V(cx,cy,cz), d)
def add(name, shape):
    o = doc.addObject("Part::Feature", name)
    o.Shape = shape.fuse() if hasattr(shape,'fuse') and False else shape
    return o

# ---------------- 1. shell main ----------------
s = box(0,0,SHELL_Z0, EX_W,EX_D,EX_H-SHELL_Z0)                 # outer
s = s.cut(box(WALL,WALL,SHELL_Z0+FLOOR_T, EX_W-2*WALL,EX_D-2*WALL, EX_H))  # cavity (open top removed below)
# top wall: re-add solid top band
s = s.fuse(box(WALL,WALL,TOP_Z1, EX_W-2*WALL,EX_D-2*WALL, EX_H-TOP_Z1))
# floor intake opening
s = s.cut(box(33,35,SHELL_Z0, 120,140, FLOOR_T+0.2))
# front bay openings
for i in range(N_BAYS):
    z = BAY_Z0 + BAY_PITCH*i
    s = s.cut(box(BAY_X0, -0.5, z, BAY_W, WALL+1, BAY_H))
# front right column: power button + LED slot + USB slot
s = s.cut(cyl(162.0, WALL/2, 150.0, 6.1, WALL+1, 'y'))          # power button
s = s.cut(box(159.0, -0.5, 60.0, 5.0, WALL+1, 80.0))            # LED strip slot
s = s.cut(box(145.0, -0.5, 122.0, 15.0, WALL+1, 8.0))           # front USB
# bay divider rails + cage side guides (structural, inside cavity)
for i in range(1, N_BAYS):                                       # between bays
    z = BAY_Z0 + BAY_PITCH*i - (BAY_PITCH-BAY_H)
    s = s.fuse(box(BAY_X0-2, WALL, z, BAY_W+4, BP_Y-WALL, BAY_PITCH-BAY_H))
s = s.fuse(box(BAY_X0-2, WALL, DECK_Z, BAY_W+4, BP_Y-WALL, BAY_Z0-DECK_Z))  # cage floor deck
for gx in (BAY_X0-2, BAY_X0+BAY_W):                              # side guide walls
    s = s.fuse(box(gx, WALL, DECK_Z, 2, BP_Y-WALL, TOP_Z1-DECK_Z))
# rear: fan circle + IO window + DC jack
s = s.cut(cyl(FAN_C[0], EX_D-WALL-0.5, FAN_C[1], FAN_R, WALL+1, 'y'))
s = s.cut(box(30.0, EX_D-WALL-0.5, 14.0, 130.0, WALL+1, 44.0))   # ITX IO shield window
s = s.cut(cyl(172.0, EX_D-WALL-0.5, 32.0, 6.6, WALL+1, 'y'))     # DC 12V jack
# side vent slots (right wall, board zone)
for k in range(5):
    s = s.cut(box(EX_W-WALL-0.5, 60.0+22.0*k, 18.0, WALL+1, 14.0, 26.0))
# top vent slots
for k in range(5):
    s = s.cut(box(30.0+26.0*k, 60.0, TOP_Z1-0.5, 6.0, 100.0, EX_H-TOP_Z1+1))
# corner screw holes through floor (join base plate)
for hx,hy in ((12,12),(EX_W-12,12),(12,EX_D-12),(EX_W-12,EX_D-12)):
    s = s.cut(cyl(hx,hy,SHELL_Z0-0.5, 2.6, FLOOR_T+1.5))
shell = add("shell_main", s)

# ---------------- 2. base plate (printed walnut-look / wood CNC later) ----------------
b = box(0,0,0, EX_W,EX_D,BASE_H)
b = b.cut(box(33,35,-0.5, 120,140, BASE_H+1))                    # intake opening (mesh glued under)
for hx,hy in ((12,12),(EX_W-12,12),(12,EX_D-12),(EX_W-12,EX_D-12)):
    b = b.cut(cyl(hx,hy,-0.5, 2.6, BASE_H+1))                    # screw holes
for cx,cy in ((20,20),(EX_W-20,20),(20,EX_D-20),(EX_W-20,EX_D-20)):
    b = b.cut(box(cx-8,cy-8,BASE_H-2.0, 16,16, 2.5))             # silicone feet recesses
base = add("base_plate", b)

# ---------------- 3. drive tray (prints x4) ----------------
# local coords: x 0..120 (width), y 0..153 (front..back), z 0..29.5
t = box(0,6,0, 120,147,2.0)                                      # floor
t = t.fuse(box(0,6,0, 2.0,147,27.5))                             # left wall
t = t.fuse(box(118,6,0, 2.0,147,27.5))                           # right wall
t = t.fuse(box(0,3,0, 120,3.0,29.5))                             # face plate
t = t.fuse(box(0,0,0, 120,3.0,2.0))                              # face skirt bottom
# handle window in face
t = t.cut(box(42,-0.5,10, 36,4.0,9.5))
# LED light-pipe hole
t = t.cut(cyl(108,1.0,24.0, 1.6, 3.0,'y'))
# latch slot (spring latch printed with tray as living-hinge stub — prototype: pocket)
t = t.cut(box(6,-0.5,22, 16,4.0,6.0))
# 3.5" snap pin bosses (2 per side)
for by in (45.0,105.0):
    t = t.fuse(cyl(2.0,by,12.0, 3.0,2.6,'x'))
    t = t.fuse(cyl(118.0,by,12.0, 3.0,2.6,'x'))
# 2.5" mount holes through floor
for hx,hy in ((30,40),(90,40),(30,110),(90,110)):
    t = t.cut(cyl(hx,hy,-0.5, 1.7, 3.0))
# rear SATA pass-through
t = t.cut(box(35,140,4, 50,14,20))
# front-to-bay guide ribs on sides
t = t.fuse(box(-0.8,20,3.0, 0.8,120,2.0))
t = t.fuse(box(120.0,20,3.0, 0.8,120,2.0))
tray = add("drive_tray", t)

# ---------------- 4. backplane bracket ----------------
bp = box(12,BP_Y,DECK_Z, 124,4,132)                              # vertical plate x12..136
for i in range(N_BAYS):                                          # SATA connector windows
    z = BAY_Z0 + BAY_PITCH*i + 7.0
    bp = bp.cut(box(58, BP_Y-0.5, z, 20, 5, 10))
bp = bp.fuse(box(8,BP_Y,DECK_Z, 4,4,132))                        # left flange
bp = bp.fuse(box(136,BP_Y,DECK_Z, 4,4,132))                      # right flange
for hx in (14,134):                                              # mount holes
    bp = bp.cut(cyl(hx,BP_Y-0.5,DECK_Z+6, 2.6, 5,'y'))
    bp = bp.cut(cyl(hx,BP_Y-0.5,DECK_Z+126, 2.6, 5,'y'))
bpb = add("backplane_bracket", bp)

# ---------------- 5. fan mount (92mm) ----------------
fm = box(FAN_C[0]-52, EX_D-WALL-6, FAN_C[1]-52, 104, 6, 104)
fm = fm.cut(cyl(FAN_C[0], EX_D-WALL-7, FAN_C[1], FAN_R-2, 8,'y'))  # fan bore
for dx,dz in ((-41.25,-41.25),(41.25,-41.25),(-41.25,41.25),(41.25,41.25)):
    fm = fm.cut(cyl(FAN_C[0]+dx, EX_D-WALL-7, FAN_C[1]+dz, 2.4, 8,'y'))  # M4 holes @82.5
fmz = add("fan_mount", fm)

# ---------------- exports ----------------
parts = {"shell_main":shell, "base_plate":base, "drive_tray":tray,
         "backplane_bracket":bpb, "fan_mount":fmz}
for name, obj in parts.items():
    Mesh.export([obj], os.path.join(STL, name + ".stl"))

Import.export(list(parts.values()), os.path.join(OUT, "assembly.step"))
doc.recompute()
doc.saveAs(os.path.join(OUT, "z1-4bay-prototype.FCStd"))

# ---------------- verification report ----------------
print("\n=== Z1 4-Bay prototype model report ===")
for name, obj in parts.items():
    bb = obj.Shape.BoundBox
    print(f"{name:20s} bbox {bb.XLength:7.2f} x {bb.YLength:7.2f} x {bb.ZLength:7.2f} mm "
          f"| volume {obj.Shape.Volume/1000:9.2f} cm3 | solids {len(obj.Shape.Solids)}")
import os.path as op
for f in sorted(os.listdir(STL)):
    print(f"STL {f:24s} {op.getsize(op.join(STL,f))/1024:9.1f} KiB")
print("STEP", op.getsize(op.join(OUT,'assembly.step'))//1024, "KiB")
print("FCStd", op.getsize(op.join(OUT,'z1-4bay-prototype.FCStd'))//1024, "KiB")
print("=== done ===")
