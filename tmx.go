// Package tmx provides a Go library that reads Tiled's TMX files.
package tmx

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

// GID constants present in decoded tiles.
const (
	GIDHorizontalFlip = 0x80000000
	GIDVerticalFlip   = 0x40000000
	GIDDiagonalFlip   = 0x20000000
	GIDFlip           = GIDHorizontalFlip | GIDVerticalFlip | GIDDiagonalFlip
	GIDMask           = 0x0fffffff
)

// Flag error values returned by various operations.
var (
	ErrUnsupportedEncoding    = errors.New("tmx: unsupported encoding scheme")
	ErrUnsupportedCompression = errors.New("tmx: unsupported compression method")
	ErrInvalidDecodedDataLen  = errors.New("tmx: invalid decoded data length")
	ErrInvalidGID             = errors.New("tmx: invalid GID")
	ErrInvalidPointsField     = errors.New("tmx: invalid points string")
)

var (
	// NilTile values are present in empty decoded map layers.
	NilTile = DecodedTile{Nil: true}
)

type (
	// GID represents a global identifier type.
	GID uint32
	// ID represents a tile ID type.
	ID uint32
)

// Map models a v1.1 XML Tiled <map>.
// See: https://doc.mapeditor.org/en/stable/reference/tmx-map-format/.
type Map struct {
	Version        string         `xml:"title,attr"`
	MapOrientation MapOrientation `xml:"orientation,attr"`
	MapRenderOrder MapRenderOrder `xml:"renderorder,attr"`
	Width          int            `xml:"width,attr"`
	Height         int            `xml:"height,attr"`
	TileWidth      int            `xml:"tilewidth,attr"`
	TileHeight     int            `xml:"tileheight,attr"`
	Properties     []Property     `xml:"properties>property"`
	Tilesets       []Tileset      `xml:"tileset"`
	Layers         []Layer        `xml:"layer"`
	ObjectGroups   []ObjectGroup  `xml:"objectgroup"`
}

// DecodedLayers decodes each map layer and returns all decoded layers.
func (m *Map) DecodedLayers() ([]DecodedLayer, error) {
	var out []DecodedLayer
	for i := 0; i < len(m.Layers); i++ {
		l := m.Layers[i]
		gids, err := l.Decode()
		if err != nil {
			return nil, err
		}

		d := DecodedLayer{}
		for j := 0; j < len(gids); j++ {
			t, err := m.DecodeGID(gids[j])
			if err != nil {
				return nil, err
			}
			d.DecodedTiles = append(d.DecodedTiles, t)
		}
		out = append(out, d)
	}
	return out, nil
}

// DecodeGID returns and decodes the tile referenced by gid or returns an error.
// The error will be ErrInvalidGID if gid is not found in m.
func (m *Map) DecodeGID(gid GID) (DecodedTile, error) {
	if gid == 0 {
		return NilTile, nil
	}

	umaskedGID := gid &^ GIDFlip

	for i := len(m.Tilesets) - 1; i >= 0; i-- {
		if m.Tilesets[i].FirstGID <= umaskedGID {
			return DecodedTile{
				ID:             ID(umaskedGID - m.Tilesets[i].FirstGID),
				Tileset:        &m.Tilesets[i],
				HorizontalFlip: gid&GIDHorizontalFlip != 0,
				VerticalFlip:   gid&GIDVerticalFlip != 0,
				DiagonalFlip:   gid&GIDDiagonalFlip != 0,
			}, nil
		}
	}

	return NilTile, ErrInvalidGID
}

func getTileset(m *Map, l DecodedLayer) (tileset *Tileset, isEmpty, usesMultipleTilesets bool) {
	for i := 0; i < len(l.DecodedTiles); i++ {
		tile := l.DecodedTiles[i]
		if !tile.Nil {
			if tileset == nil {
				tileset = tile.Tileset
			} else if tileset != tile.Tileset {
				return tileset, false, true
			}
		}
	}

	if tileset == nil {
		return nil, true, false
	}

	return tileset, false, false
}

// MapOrientation represents an layout for map tiles.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#map.
type MapOrientation string

// Valid map orientations.
const (
	MapOrthogonal MapOrientation = "orthogonal"
	MapIsometric  MapOrientation = "isometric"
	MapStaggered  MapOrientation = "staggered"
	MapHexagonal  MapOrientation = "hexagonal"
)

// MapRenderOrder represents an order for rendering map tiles.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#map.
type MapRenderOrder string

// Valid render orders.
const (
	RenderRightDown MapRenderOrder = "right-down"
	RenderRightUp   MapRenderOrder = "right-up"
	RenderLeftDown  MapRenderOrder = "left-down"
	RenderLeftUp    MapRenderOrder = "left-up"
)

// Tileset models a v1.1 XML <tileset>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#tileset.
type Tileset struct {
	FirstGID   GID        `xml:"firstgid,attr"`
	Source     string     `xml:"source,attr"`
	Name       string     `xml:"name,attr"`
	TileWidth  int        `xml:"tilewidth,attr"`
	TileHeight int        `xml:"tileheight,attr"`
	Spacing    int        `xml:"spacing,attr"`
	Margin     int        `xml:"margin,attr"`
	Tilecount  int        `xml:"tilecount,attr"`
	Columns    int        `xml:"columns,attr"`
	TileOffset TileOffset `xml:"tileoffset"`
	Grid       Grid       `xml:"grid"`
	Properties []Property `xml:"properties>property"`
	Image      Image      `xml:"image"`
	Terrains   []Terrain  `xml:"terraintypes>terrain"`
	Tiles      []Tile     `xml:"tile"`
	WangSets   []WangSet  `xml:"wangsets>wangset"`
}

// TileOffset models a v1 tileset <tileoffset>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#tileoffset.
type TileOffset struct {
	X int `xml:"x,attr"`
	Y int `xml:"y,attr"`
}

// Grid models v1 tileset <grid> settings.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#grid.
type Grid struct {
	TileOrientation TileOrientation `xml:"orientation,attr"`
	Width           int             `xml:"width,attr"`
	Height          int             `xml:"height,attr"`
}

// TileOrientation represents layouts for tilesets.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#grid.
type TileOrientation string

// Valid tileset orientations.
const (
	TileOrthoganal TileOrientation = "orthoganal"
	TileIsometric  TileOrientation = "isometric"
)

// Property models a v1 named <property>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#property.
type Property struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// Terrain models a v1 tileset <terrain>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#terrain.
type Terrain struct {
	Name       string     `xml:"name,attr"`
	TileID     ID         `xml:"tile,attr"`
	Properties []Property `xml:"properties>property"`
}

// WangSet models a v1 tileset <wangset>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#wangset.
type WangSet struct {
	Name    string      `xml:"name,attr"`
	TileID  ID          `xml:"tile,attr"`
	Corners []WangColor `xml:"wangcornercolor"`
	Edges   []WangColor `xml:"wangedgecolor"`
	Tiles   []WangTile  `xml:"wangtile"`
}

// WangColor models a v1.1 wangset color.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#wangcornercolor.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#wangedgecolor.
type WangColor struct {
	Name        string  `xml:"name,attr"`
	Color       string  `xml:"color,attr"`
	TileID      ID      `xml:"tile,attr"`
	Probability float32 `xml:"probability,attr"`
}

// WangTile models a v1.1 wangset tile.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#wangtile.
type WangTile struct {
	TileID ID     `xml:"tileid,attr"`
	WangID uint32 `xml:"wangid,attr"`
}

// Tile models a v1.0 <tile>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#tile
type Tile struct {
	ID           ID            `xml:"id,attr"`
	Type         string        `xml:"type,attr"`
	Terrain      string        `xml:"terrain,attr"`
	Probability  float32       `xml:"probability,attr"`
	Properties   []Property    `xml:"properties>property"`
	Image        Image         `xml:"image"` // Unset if using tileset image.
	ObjectGroups []ObjectGroup `xml:"objectgroup"`
	Animation    Animation     `xml:"animation"`
}

// Image models a v1 tile <image>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#image.
type Image struct {
	Source string `xml:"source,attr"`
	Trans  string `xml:"trans,attr"`
	Width  int    `xml:"width,attr"`
	Height int    `xml:"height,attr"`
}

// Animation models a v1 tile <animation>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#animation.
type Animation struct {
	Frames []Frame `xml:"frames>frame"`
}

// Frame models a v1 tile animation <frame>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#frame.
type Frame struct {
	TileID   ID  `xml:"tileid,attr"`
	Duration int `xml:"duration,attr"`
}

// Layer models a v1.2 map layer.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#layer.
type Layer struct {
	ID         ID         `xml:"id,attr"`
	Name       string     `xml:"name,attr"`
	Width      int        `xml:"width,attr"`
	Height     int        `xml:"height,attr"`
	Opacity    float32    `xml:"opacity,attr"`
	Visible    bool       `xml:"visible,attr"`
	OffsetX    int        `xml:"offsetx,attr"`
	OffsetY    int        `xml:"offsety,attr"`
	Properties []Property `xml:"properties>property"`
	Data       Data       `xml:"data"`
}

// Data models v1 map layer data.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#data.
type Data struct {
	Encoding    LayerEncoding    `xml:"encoding,attr"`
	Compression LayerCompression `xml:"compression,attr"`
	Bytes       []byte           `xml:",innerxml"`
}

// Decode and decompress the data object to yield a slice of tile GIDs.
func (l Layer) Decode() ([]GID, error) {
	dataBytes, err := l.Data.decodeBytes()
	if err != nil {
		return nil, err
	}

	if len(dataBytes) != l.Width*l.Height*4 {
		return nil, ErrInvalidDecodedDataLen
	}

	gids := make([]GID, l.Width*l.Height)

	j := 0
	for y := 0; y < l.Height; y++ {
		for x := 0; x < l.Width; x++ {
			gid := GID(dataBytes[j]) +
				GID(dataBytes[j+1])<<8 +
				GID(dataBytes[j+2])<<16 +
				GID(dataBytes[j+3])<<24
			j += 4

			gids[y*l.Width+x] = gid
		}
	}

	return gids, nil
}

func (d Data) decodeBytes() ([]byte, error) {
	encoder := base64.NewDecoder(
		base64.StdEncoding,
		bytes.NewReader(bytes.TrimSpace(d.Bytes)))

	var err error
	var zr io.Reader
	switch d.Compression {
	case Gzip:
		zr, err = gzip.NewReader(encoder)
	case Zlib:
		zr, err = zlib.NewReader(encoder)
	default:
		return nil, ErrUnsupportedCompression
	}
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(zr)
}

// LayerEncoding represents the type of encoding used in tile layer data.
type LayerEncoding string

// Various layer encodings.
const (
	XML    LayerEncoding = ""    // unsupported
	CSV    LayerEncoding = "csv" // unsupported
	Base64 LayerEncoding = "base64"
)

// LayerCompression represents the type of compression used in tile layers.
type LayerCompression string

// Supported layer compression types.
const (
	Uncompressed LayerCompression = ""
	Gzip         LayerCompression = "gzip"
	Zlib         LayerCompression = "zlib"
)

// DecodedLayer is outputted from the layer <data> decoder.
type DecodedLayer struct {
	DecodedTiles []DecodedTile // Tile entry (x,y) is at l.DecodedTiles[y*map.Width+x].
	Tileset      *Tileset      // Only set when the layer uses a single tileset and Empty is false.
	Empty        bool          // Set when all entries of the layer are NilTile.
}

// DecodedTile is outputted from the layer <data> decoder.
type DecodedTile struct {
	ID             ID
	Tileset        *Tileset
	HorizontalFlip bool
	VerticalFlip   bool
	DiagonalFlip   bool
	Nil            bool
}

// IsNil returns whether the tile contains no data.
func (t DecodedTile) IsNil() bool {
	return t.Nil
}

// ObjectGroup models a v1.2 map <objectgroup>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#objectgroup.
type ObjectGroup struct {
	ID         ID         `xml:"id,attr"`
	Name       string     `xml:"name,attr"`
	Color      string     `xml:"color,attr"`
	Opacity    float32    `xml:"opacity,attr"`
	Visible    bool       `xml:"visible,attr"`
	Properties []Property `xml:"properties>property"`
	Objects    []Object   `xml:"object"`
}

// Object models a v1.2 object group <object>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#object.
type Object struct {
	ID         ID         `xml:"id,attr"`
	Name       string     `xml:"name,attr"`
	Type       string     `xml:"type,attr"`
	X          float64    `xml:"x,attr"`
	Y          float64    `xml:"y,attr"`
	Width      float64    `xml:"width,attr"`
	Height     float64    `xml:"height,attr"`
	Rotation   float64    `xml:"rotation,attr"`
	GID        int        `xml:"gid,attr"`
	Visible    bool       `xml:"visible,attr"`
	Polygons   []Polygon  `xml:"polygon"`
	PolyLines  []Polygon  `xml:"polyline"`
	Properties []Property `xml:"properties>property"`
}

// Polygon models a v1 object <polygon> or <polyline>.
// See: https://doc.mapeditor.org/de/stable/reference/tmx-map-format/#polygon.
type Polygon struct {
	Points string `xml:"points,attr"`
}

// Point represents a 2D point in a decoded polygon.
type Point struct {
	X int
	Y int
}

// Decode and return a slice of points from the polygon.
func (p Polygon) Decode() ([]Point, error) {
	parts := strings.Split(p.Points, " ")
	out := make([]Point, len(parts))

	for i, part := range parts {
		coords := strings.Split(part, ",")
		if len(coords) != 2 {
			return nil, ErrInvalidPointsField
		}

		var err error
		if out[i].X, err = strconv.Atoi(coords[0]); err != nil {
			return nil, err
		}
		if out[i].Y, err = strconv.Atoi(coords[1]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Read a map from the reader r or returns an error.
func Read(r io.Reader) (*Map, error) {
	d := xml.NewDecoder(r)
	out := new(Map)

	if err := d.Decode(out); err != nil {
		return nil, err
	}

	layers, err := out.DecodedLayers()
	if err != nil {
		return nil, err
	}

	for i := 0; i < len(layers); i++ {
		l := layers[i]

		tileset, isEmpty, usesMultipleTilesets := getTileset(out, l)
		if usesMultipleTilesets {
			continue
		}
		l.Empty, l.Tileset = isEmpty, tileset
	}

	return out, nil
}

// ReadFile reads a map from a file path or returns an error.
func ReadFile(filepath string) (*Map, error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out, err := Read(f)
	if err != nil {
		return nil, err
	}
	return out, err
}
