package validation

import (
	"fmt"
	"net/mail"

	"warta/internal/dto"
	"warta/internal/model"
)

// bcrypt hanya memakai 72 byte pertama password, sisanya diabaikan diam-diam.
const maxPasswordBytes = 72

func ValidateRegister(r dto.RegisterRequest) map[string]string {
	problems := make(map[string]string)
	nameProblem(problems, r.Name)
	emailProblem(problems, r.Email)
	passwordProblem(problems, "password", r.Password)
	return problems
}

func ValidateLogin(r dto.LoginRequest) map[string]string {
	problems := make(map[string]string)
	if r.Email == "" {
		problems["email"] = "email wajib diisi"
	}
	if r.Password == "" {
		problems["password"] = "password wajib diisi"
	}
	return problems
}

const maxBio = 300

func ValidateProfile(r dto.UpdateProfileRequest) map[string]string {
	problems := make(map[string]string)
	if r.Name != nil {
		nameProblem(problems, *r.Name)
	}
	if r.Bio != nil && length(*r.Bio) > maxBio {
		problems["bio"] = fmt.Sprintf("bio maksimal %d karakter", maxBio)
	}
	if r.AvatarURL != nil && *r.AvatarURL != "" && !coverPattern.MatchString(*r.AvatarURL) {
		problems["avatar_url"] = "avatar_url harus berupa path hasil upload"
	}
	return problems
}

func ValidateChangePassword(r dto.ChangePasswordRequest) map[string]string {
	problems := make(map[string]string)
	if r.CurrentPassword == "" {
		problems["current_password"] = "current_password wajib diisi"
	}
	passwordProblem(problems, "new_password", r.NewPassword)
	if _, bad := problems["new_password"]; !bad && r.NewPassword == r.CurrentPassword {
		problems["new_password"] = "new_password harus berbeda dari password sekarang"
	}
	return problems
}

func ValidateForgotPassword(r dto.ForgotPasswordRequest) map[string]string {
	problems := make(map[string]string)
	emailProblem(problems, r.Email)
	return problems
}

func ValidateResetPassword(r dto.ResetPasswordRequest) map[string]string {
	problems := make(map[string]string)
	if r.Token == "" {
		problems["token"] = "token wajib diisi"
	}
	passwordProblem(problems, "new_password", r.NewPassword)
	return problems
}

func ValidateRole(r dto.UpdateRoleRequest) map[string]string {
	problems := make(map[string]string)
	switch {
	case r.Role == "":
		problems["role"] = "role wajib diisi"
	case !model.Role(r.Role).Valid():
		problems["role"] = "role harus admin, author, atau reader"
	}
	return problems
}

func nameProblem(problems map[string]string, name string) {
	switch n := length(name); {
	case n == 0:
		problems["name"] = "name wajib diisi"
	case n < 2:
		problems["name"] = "name minimal 2 karakter"
	case n > 100:
		problems["name"] = "name maksimal 100 karakter"
	}
}

func emailProblem(problems map[string]string, email string) {
	if email == "" {
		problems["email"] = "email wajib diisi"
		return
	}
	if len(email) > 191 {
		problems["email"] = "email maksimal 191 karakter"
		return
	}
	// ParseAddress juga menerima bentuk "Nama <alamat>", jadi hasilnya harus
	// sama persis dengan input.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		problems["email"] = "email tidak valid"
	}
}

func passwordProblem(problems map[string]string, field, password string) {
	switch {
	case password == "":
		problems[field] = field + " wajib diisi"
	case length(password) < 8:
		problems[field] = field + " minimal 8 karakter"
	case len(password) > maxPasswordBytes:
		problems[field] = field + " maksimal 72 byte"
	}
}
