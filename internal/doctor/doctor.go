package doctor

type Status string

const (
	StatusOK   Status = "OK"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

type Check struct {
	Status  Status
	Name    string
	Message string
}

type Report struct {
	Checks []Check
}

func NewReport() *Report {
	return &Report{}
}

func (r *Report) OK(name, message string) {
	r.Checks = append(r.Checks, Check{Status: StatusOK, Name: name, Message: message})
}

func (r *Report) Warn(name, message string) {
	r.Checks = append(r.Checks, Check{Status: StatusWarn, Name: name, Message: message})
}

func (r *Report) Fail(name, message string) {
	r.Checks = append(r.Checks, Check{Status: StatusFail, Name: name, Message: message})
}

func (r *Report) Success() bool {
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			return false
		}
	}
	return true
}
