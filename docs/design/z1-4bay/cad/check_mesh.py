import Mesh, os
os.chdir(os.path.dirname(os.path.abspath(__file__)))
for f in sorted(os.listdir('stl')):
    m = Mesh.Mesh(os.path.join('stl', f))
    try:
        solid = m.isSolid()
    except Exception as e:
        solid = "err:%s" % e
    print("CHECK %s tris=%d solid=%s" % (f, m.CountFacets, solid))
