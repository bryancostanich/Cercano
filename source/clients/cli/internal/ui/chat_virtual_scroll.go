package ui

// virtualScroll is the transcript scroll model used by the virtualized chat
// viewport. It intentionally mirrors the tiny subset of Bubble viewport
// behavior that chatView depends on, without owning or splitting a giant content
// string.
type virtualScroll struct {
	width      int
	height     int
	yOffset    int
	totalLines int
}

func newVirtualScroll(width, height int) virtualScroll {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	return virtualScroll{width: width, height: height}
}

func (s *virtualScroll) Width() int  { return s.width }
func (s *virtualScroll) Height() int { return s.height }

func (s *virtualScroll) SetSize(width, height int) {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	s.width = width
	s.height = height
	s.Clamp()
}

func (s *virtualScroll) TotalLineCount() int { return s.totalLines }
func (s *virtualScroll) YOffset() int        { return s.yOffset }

func (s *virtualScroll) SetTotalLineCount(total int) {
	if total < 0 {
		total = 0
	}
	s.totalLines = total
	s.Clamp()
}

func (s *virtualScroll) SetYOffset(n int) {
	s.yOffset = n
	s.Clamp()
}

func (s *virtualScroll) AtBottom() bool { return s.yOffset >= s.maxYOffset() }

func (s *virtualScroll) GotoBottom() { s.yOffset = s.maxYOffset() }

func (s *virtualScroll) ScrollUp(n int) {
	if n < 0 {
		s.ScrollDown(-n)
		return
	}
	s.SetYOffset(s.yOffset - n)
}

func (s *virtualScroll) ScrollDown(n int) {
	if n < 0 {
		s.ScrollUp(-n)
		return
	}
	s.SetYOffset(s.yOffset + n)
}

func (s *virtualScroll) Clamp() {
	maxY := s.maxYOffset()
	if s.yOffset < 0 {
		s.yOffset = 0
	} else if s.yOffset > maxY {
		s.yOffset = maxY
	}
}

func (s *virtualScroll) maxYOffset() int {
	if s.height <= 0 || s.totalLines <= s.height {
		return 0
	}
	return s.totalLines - s.height
}
